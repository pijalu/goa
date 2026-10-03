// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
)

// Image paste over the web (spec §7.5/§11.4). A browser cannot put a PNG on the
// agent's clipboard the way a real terminal paste does, so the page uploads the
// image bytes to /upload and inserts the returned *path* into the input line —
// exactly the text the terminal editor inserts for a clipboard image, so the
// rest of the pipeline (submit → splitUserInput → extractImagePaths → agent
// attachment) is the same code in both.

// MaxUploadBytes bounds one pasted image. A screenshot is well under it; the
// limit exists so an upload cannot fill the disk.
const MaxUploadBytes = 16 << 20 // 16 MiB

// errNotImage is returned for an upload whose bytes are not a known image
// format, whatever its declared Content-Type says.
var errNotImage = errors.New("unsupported upload: not an image")

// uploadResponse is the JSON body returned to the page.
type uploadResponse struct {
	// Path is the temp file the image was written to; the page inserts it as
	// input text, exactly as a terminal clipboard paste would.
	Path string `json:"path"`
	// Bytes is the stored size, echoed so the client can log what it sent.
	Bytes int64 `json:"bytes"`
}

// handleUpload accepts a multipart image paste and answers with the stored path.
//
// It is a real endpoint with real limits: the bytes must sniff as an image and
// the body is capped — a browser-reachable session must not become an arbitrary
// file-write primitive.
func (s *Server) handleUpload(w http.ResponseWriter, r *http.Request) {
	if s.opts.ReadOnly {
		http.Error(w, "input rejected: read-only mode", http.StatusForbidden)
		return
	}
	if err := r.ParseMultipartForm(MaxUploadBytes); err != nil {
		http.Error(w, "not a multipart form", http.StatusBadRequest)
		return
	}
	file, _, err := r.FormFile("file")
	if err != nil {
		http.Error(w, `missing "file" field`, http.StatusBadRequest)
		return
	}
	defer file.Close()

	path, n, err := saveUploadedImage(file)
	if err != nil {
		http.Error(w, err.Error(), http.StatusUnsupportedMediaType)
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(uploadResponse{Path: path, Bytes: n})
}

// saveUploadedImage validates and stores one uploaded image, returning the path
// the caller should hand to the input line. The extension comes from the
// sniffed content, never from the client-supplied filename or Content-Type, so
// a crafted multipart part can neither choose the on-disk extension nor smuggle
// a non-image past the check.
func saveUploadedImage(r io.Reader) (string, int64, error) {
	head := make([]byte, sniffLen)
	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return "", 0, fmt.Errorf("read upload: %w", err)
	}
	head = head[:n]

	// Only the bytes decide. A declared Content-Type is attacker-controlled,
	// so trusting it as a fallback would let any file be stored as an image.
	ext := detectImageExt(head)
	if ext == "" {
		return "", 0, errNotImage
	}

	body := io.MultiReader(strings.NewReader(string(head)), r)
	f, err := os.CreateTemp("", "goa-upload-*"+ext)
	if err != nil {
		return "", 0, fmt.Errorf("create temp file: %w", err)
	}
	defer f.Close()
	written, err := io.Copy(f, io.LimitReader(body, MaxUploadBytes+1))
	if err != nil {
		os.Remove(f.Name())
		return "", 0, fmt.Errorf("write upload: %w", err)
	}
	if written > MaxUploadBytes {
		os.Remove(f.Name())
		return "", 0, fmt.Errorf("image larger than %d bytes", MaxUploadBytes)
	}
	return f.Name(), written, nil
}

// sniffLen is how many leading bytes identify every format we accept (the
// longest signature, RIFF….WEBP, is 12 bytes; 64 leaves room to spare).
const sniffLen = 64

// imageSigs lists the accepted formats by magic number. The declared MIME is
// never consulted, so a lying Content-Type cannot smuggle a non-image past the
// check.
var imageSigs = []struct {
	ext    string
	sig    string
	offset int
}{
	{ext: ".png", sig: "\x89PNG\r\n\x1a\n"},
	{ext: ".jpg", sig: "\xff\xd8\xff"},
	{ext: ".gif", sig: "GIF87a"},
	{ext: ".gif", sig: "GIF89a"},
	{ext: ".webp", sig: "WEBP", offset: 8},
}

// detectImageExt maps the leading bytes of a file to an extension.
func detectImageExt(head []byte) string {
	for _, s := range imageSigs {
		sig := []byte(s.sig)
		end := s.offset + len(sig)
		if len(head) >= end && string(head[s.offset:end]) == s.sig {
			return s.ext
		}
	}
	return ""
}
