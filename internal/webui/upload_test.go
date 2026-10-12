// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"bytes"
	"encoding/json"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/textproto"
	"os"
	"strings"
	"testing"
)

// A pasted image becomes a temp file whose path the page inserts into the input
// line — the same text the terminal editor inserts for a clipboard image, so the
// submit → attachment pipeline downstream is unchanged.
func TestUpload_StoresImageAndReturnsPath(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	png := []byte("\x89PNG\r\n\x1a\n" + strings.Repeat("x", 64))

	body, ctype := multipartBody(t, "screenshot.png", "image/png", png)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, multipartRequest(body, ctype))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content type = %q, want application/json", ct)
	}
	var out uploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !strings.HasSuffix(out.Path, ".png") {
		t.Errorf("path %q does not carry the sniffed extension", out.Path)
	}
	if out.Bytes != int64(len(png)) {
		t.Errorf("bytes = %d, want %d", out.Bytes, len(png))
	}
	t.Cleanup(func() { os.Remove(out.Path) })

	stored, err := os.ReadFile(out.Path)
	if err != nil {
		t.Fatalf("read stored upload: %v", err)
	}
	if !bytes.Equal(stored, png) {
		t.Errorf("stored bytes differ from the upload (%d vs %d bytes)", len(stored), len(png))
	}
}

// The on-disk extension comes from the sniffed content, never the client's
// filename, so a crafted part cannot choose where it lands.
func TestUpload_ExtensionComesFromBytes(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	jpeg := []byte{0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10}

	body, ctype := multipartBody(t, "payload.png", "image/png", jpeg)
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, multipartRequest(body, ctype))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body %q)", rec.Code, rec.Body.String())
	}
	var out uploadResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	t.Cleanup(func() { os.Remove(out.Path) })
	if !strings.HasSuffix(out.Path, ".jpg") {
		t.Errorf("path %q, want a .jpg derived from the bytes", out.Path)
	}
}

// A browser-reachable session must not become an arbitrary file-write
// primitive: non-image bytes are refused whatever the declared type says.
func TestUpload_RejectsNonImage(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	body, ctype := multipartBody(t, "evil.png", "image/png", []byte("#!/bin/sh\nrm -rf /\n"))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, multipartRequest(body, ctype))

	if rec.Code != http.StatusUnsupportedMediaType {
		t.Fatalf("status = %d, want 415", rec.Code)
	}
}

// A request that is not a multipart form at all is a client error, not an
// unsupported media type.
func TestUpload_RejectsNonMultipart(t *testing.T) {
	srv, _ := newTestServer(t, 40, 10)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/upload", strings.NewReader("raw"))
	req.Header.Set("Content-Type", "text/plain")
	srv.Handler().ServeHTTP(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}

// --read-only refuses uploads outright: a viewer cannot write to the host.
func TestUpload_RefusedInReadOnlyMode(t *testing.T) {
	vt := NewVirtualTerminal(20, 2)
	srv := NewServer(vt, 20, 2, ServerOptions{
		SessionID: func() string { return "sess-1" },
		ReadOnly:  true,
		Logger:    discardLogger(),
	})
	t.Cleanup(func() { _ = srv.Close() })

	body, ctype := multipartBody(t, "a.png", "image/png", []byte("\x89PNG\r\n\x1a\nrest"))
	rec := httptest.NewRecorder()
	srv.Handler().ServeHTTP(rec, multipartRequest(body, ctype))

	if rec.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want 403", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "read-only") {
		t.Errorf("body %q does not explain the refusal", rec.Body.String())
	}
}

func TestDetectImageExt(t *testing.T) {
	cases := []struct {
		name string
		head []byte
		want string
	}{
		{"png", []byte("\x89PNG\r\n\x1a\n..."), ".png"},
		{"jpeg", []byte{0xFF, 0xD8, 0xFF, 0xE0}, ".jpg"},
		{"gif87", []byte("GIF87a..."), ".gif"},
		{"gif89", []byte("GIF89a..."), ".gif"},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBPVP8 "), ".webp"},
		{"text", []byte("hello"), ""},
		{"elf", []byte("\x7fELF\x02\x01"), ""},
		{"empty", nil, ""},
		{"truncated png", []byte("\x89PNG"), ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := detectImageExt(tc.head); got != tc.want {
				t.Errorf("detectImageExt(%q) = %q, want %q", tc.head, got, tc.want)
			}
		})
	}
}

// multipartRequest wraps a multipart body in a POST request carrying its
// boundary content type, exactly as a browser sends it.
func multipartRequest(body *bytes.Buffer, ctype string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/upload", body)
	req.Header.Set("Content-Type", ctype)
	return req
}

// multipartBody builds a one-file multipart request body, declaring the part's
// content type the way a browser does.
func multipartBody(t *testing.T, filename, contentType string, data []byte) (*bytes.Buffer, string) {
	t.Helper()
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	h := make(textproto.MIMEHeader)
	h.Set("Content-Disposition", `form-data; name="file"; filename="`+filename+`"`)
	h.Set("Content-Type", contentType)
	part, err := mw.CreatePart(h)
	if err != nil {
		t.Fatalf("create part: %v", err)
	}
	if _, err := part.Write(data); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close multipart: %v", err)
	}
	return &buf, mw.FormDataContentType()
}
