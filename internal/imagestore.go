// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"
)

// Pasted images are referenced from conversation history by path, so the file
// must outlive the paste: the provider re-reads it whenever it builds a request,
// and a resumed session replays the same path. os.TempDir is therefore the
// wrong home — a reboot, a tmp cleaner or a long idle period destroys the
// attachment and the image silently disappears from every later request. The
// durable user cache directory survives all three.

// imageStoreDirName is the subdirectory of the user cache directory that holds
// pasted/uploaded images.
const imageStoreDirName = "goa/images"

// ImageStoreDir returns the directory pasted and uploaded images are written to,
// creating it when missing.
func ImageStoreDir() (string, error) {
	base, err := os.UserCacheDir()
	if err != nil || base == "" {
		// A platform without a cache dir (rare) still gets a stable location
		// rather than a per-process temp path.
		base = filepath.Join(os.TempDir(), "goa-cache")
	}
	dir := filepath.Join(base, imageStoreDirName)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return "", fmt.Errorf("create image store: %w", err)
	}
	return dir, nil
}

// NewImageFile creates a uniquely named file with the given extension (".png",
// ".jpg", …) inside the image store. The caller owns the returned file and must
// close it.
func NewImageFile(ext string) (*os.File, error) {
	dir, err := ImageStoreDir()
	if err != nil {
		return nil, err
	}
	f, err := os.CreateTemp(dir, "goa-image-*"+ext)
	if err != nil {
		return nil, fmt.Errorf("create image file: %w", err)
	}
	return f, nil
}

// ImageStoreLifetime is how long a stored image is kept. Long enough that a
// resumed session still finds its attachments, short enough that the store
// cannot grow without bound.
const ImageStoreLifetime = 30 * 24 * time.Hour

// PruneImages deletes stored images older than ImageStoreLifetime and returns
// how many were removed. Called opportunistically at startup; a pruning failure
// is never fatal (the store is a cache, not a source of truth).
func PruneImages() int {
	dir, err := ImageStoreDir()
	if err != nil {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	cutoff := time.Now().Add(-ImageStoreLifetime)
	removed := 0
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		info, err := e.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if os.Remove(filepath.Join(dir, e.Name())) == nil {
			removed++
		}
	}
	return removed
}

// imageSigs lists the accepted image formats by magic number. A declared MIME
// type or filename extension is never consulted, so a lying Content-Type cannot
// smuggle a non-image past the check.
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

// SniffLen is how many leading bytes identify every accepted format (the
// longest signature, RIFF….WEBP, is 12 bytes; 64 leaves room to spare). Callers
// that read a stream to classify it read this many bytes first.
const SniffLen = 64

// ErrNotImage reports content that is not in a format the image store keeps.
var ErrNotImage = errors.New("not a recognised image format")

// ErrImageTooLarge reports content above the caller's byte cap.
var ErrImageTooLarge = errors.New("image too large")

// StoreImage writes an image into the durable image store and returns its path
// and total size. head holds the leading bytes already read (they decide the
// format, and are part of what is stored); body supplies the rest. At most
// maxBytes are written: larger content is rejected with ErrImageTooLarge and
// leaves no file behind, and content that does not sniff as an image is rejected
// with ErrNotImage.
//
// This is the single writer for images that arrive from outside — a terminal
// clipboard paste and a web upload both go through it — so the two paths share
// one format table, one size rule and one store, and cannot drift apart.
func StoreImage(head []byte, body io.Reader, maxBytes int64) (string, int64, error) {
	ext := SniffImageExt(head)
	if ext == "" {
		return "", 0, ErrNotImage
	}
	f, err := NewImageFile(ext)
	if err != nil {
		return "", 0, err
	}
	defer f.Close()
	// head is part of the content, not just of the sniff: it was read off the
	// stream by the caller, so it has to be written back first.
	if _, err := f.Write(head); err != nil {
		os.Remove(f.Name())
		return "", 0, fmt.Errorf("write image: %w", err)
	}
	// The cap covers head + body, so read one byte past what is left of it and
	// let that byte prove the content is over the limit.
	room := maxBytes + 1 - int64(len(head))
	if room < 0 {
		room = 0
	}
	written, err := io.Copy(f, io.LimitReader(body, room))
	total := int64(len(head)) + written
	if err != nil {
		os.Remove(f.Name())
		return "", 0, fmt.Errorf("write image: %w", err)
	}
	if total > maxBytes {
		os.Remove(f.Name())
		return "", 0, fmt.Errorf("image larger than %d bytes: %w", maxBytes, ErrImageTooLarge)
	}
	return f.Name(), total, nil
}

// SniffImageExt maps the leading bytes of a file to an image extension, or ""
// when they match no accepted format.
func SniffImageExt(head []byte) string {
	for _, s := range imageSigs {
		sig := []byte(s.sig)
		end := s.offset + len(sig)
		if len(head) >= end && string(head[s.offset:end]) == s.sig {
			return s.ext
		}
	}
	return ""
}
