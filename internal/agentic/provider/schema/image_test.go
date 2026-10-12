// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

import (
	"bytes"
	"encoding/base64"
	"image"
	"image/color"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// pngBytes encodes img as PNG.
func pngBytes(t *testing.T, img image.Image) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, img); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// writeFile writes data into dir/name and returns the path.
func writeFile(t *testing.T, dir, name string, data []byte) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestSniffImageMime(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"png", []byte("\x89PNG\r\n\x1a\n...."), "image/png"},
		{"jpeg", []byte("\xff\xd8\xff\xe0"), "image/jpeg"},
		{"gif87", []byte("GIF87a...."), "image/gif"},
		{"gif89", []byte("GIF89a...."), "image/gif"},
		{"webp", []byte("RIFF....WEBPVP8 "), "image/webp"},
		{"text", []byte("hello world"), ""},
		{"empty", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SniffImageMime(tc.data); got != tc.want {
				t.Errorf("SniffImageMime(%q) = %q, want %q", tc.data, got, tc.want)
			}
		})
	}
}

// TestEncodeImage_Path covers the common case: a real file is read, sniffed and
// Base64-encoded with its MIME type — the two fields every vision API needs and
// which the Anthropic/Google adapters used to leave as a raw path.
func TestEncodeImage_Path(t *testing.T) {
	raw := pngBytes(t, image.NewRGBA(image.Rect(0, 0, 2, 2)))
	path := writeFile(t, t.TempDir(), "shot.png", raw)

	enc, ok := EncodeImage(path)
	if !ok {
		t.Fatal("EncodeImage returned ok=false for a valid PNG")
	}
	if enc.MimeType != "image/png" {
		t.Errorf("MimeType = %q, want image/png", enc.MimeType)
	}
	got, err := base64.StdEncoding.DecodeString(enc.Base64)
	if err != nil {
		t.Fatalf("Base64 is not decodable: %v", err)
	}
	if !bytes.Equal(got, raw) {
		t.Error("decoded Base64 does not round-trip the file bytes")
	}
}

func TestEncodeImage_Rejections(t *testing.T) {
	dir := t.TempDir()
	notImage := writeFile(t, dir, "notes.png", []byte("plain text, no magic"))

	tests := []struct {
		name string
		src  string
	}{
		{"missing file", filepath.Join(dir, "absent.png")},
		{"not an image", notImage},
		{"empty", ""},
		{"directory", dir},
		{"malformed data url", "data:image/png;base64"},
		{"non-base64 data url", "data:image/png,plain"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, ok := EncodeImage(tc.src); ok {
				t.Errorf("EncodeImage(%q) = ok, want rejection", tc.src)
			}
		})
	}
}

// TestEncodeImage_DataURLPassthrough keeps an already-encoded attachment from
// being re-read (it has no file behind it) or double-encoded.
func TestEncodeImage_DataURLPassthrough(t *testing.T) {
	src := "data:image/jpeg;base64,QUJD"

	enc, ok := EncodeImage(src)
	if !ok {
		t.Fatal("EncodeImage rejected a well-formed data URL")
	}
	if enc.Base64 != "QUJD" || enc.MimeType != "image/jpeg" {
		t.Errorf("EncodeImage(%q) = %+v, want passthrough", src, enc)
	}
	if got := enc.DataURL(); got != src {
		t.Errorf("DataURL() = %q, want %q", got, src)
	}
}

func TestImageToDataURL(t *testing.T) {
	raw := pngBytes(t, image.NewRGBA(image.Rect(0, 0, 1, 1)))
	path := writeFile(t, t.TempDir(), "a.png", raw)

	got := ImageToDataURL(path)
	if !strings.HasPrefix(got, "data:image/png;base64,") {
		t.Errorf("ImageToDataURL = %q, want a PNG data URL", got)
	}
	if ImageToDataURL(filepath.Join(t.TempDir(), "nope.png")) != "" {
		t.Error("ImageToDataURL should be empty for an unreadable source")
	}
}

// TestFitImage_SmallImageUnchanged keeps the common path free of a needless
// re-encode: an image already inside the limits must come back byte-identical.
func TestFitImage_SmallImageUnchanged(t *testing.T) {
	raw := pngBytes(t, image.NewRGBA(image.Rect(0, 0, 8, 8)))

	out, mime := FitImage(raw, "image/png")

	if !bytes.Equal(out, raw) || mime != "image/png" {
		t.Error("FitImage modified an image that already fits the limits")
	}
}

// TestFitImage_ClampsOversizedDimension is the regression test for the 8000 px
// ceiling: providers reject a wider image outright.
func TestFitImage_ClampsOversizedDimension(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, MaxImageDimension+500, 4))
	raw := pngBytes(t, img)

	out, mime := FitImage(raw, "image/png")
	if mime != "image/png" {
		t.Fatalf("mime = %q, want image/png after re-encode", mime)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(out))
	if err != nil {
		t.Fatalf("resized output is not decodable: %v", err)
	}
	if cfg.Width > MaxImageDimension {
		t.Errorf("width = %d, want <= %d", cfg.Width, MaxImageDimension)
	}
}

// TestFitImage_ClampsOversizedBytes drives the byte budget with incompressible
// noise so the PNG cannot sneak under the limit.
func TestFitImage_ClampsOversizedBytes(t *testing.T) {
	const size = 1400
	img := image.NewRGBA(image.Rect(0, 0, size, size))
	seed := uint32(12345)
	for y := 0; y < size; y++ {
		for x := 0; x < size; x++ {
			seed = seed*1664525 + 1013904223
			img.SetRGBA(x, y, color.RGBA{R: uint8(seed >> 24), G: uint8(seed >> 16), B: uint8(seed >> 8), A: 255})
		}
	}
	raw := pngBytes(t, img)
	if len(raw) <= MaxImageBytes {
		t.Skipf("noise PNG only %d bytes, cannot exercise the byte limit", len(raw))
	}

	out, _ := FitImage(raw, "image/png")
	if len(out) > MaxImageBytes {
		t.Errorf("encoded size = %d, want <= %d", len(out), MaxImageBytes)
	}
	if _, _, err := image.Decode(bytes.NewReader(out)); err != nil {
		t.Errorf("resized output is not decodable: %v", err)
	}
}

// TestFitImage_UndecodablePassesThrough documents the deliberate WebP behaviour:
// without a registered decoder the bytes are handed on unchanged so the provider
// reports the real problem instead of the image vanishing here.
func TestFitImage_UndecodablePassesThrough(t *testing.T) {
	data := []byte("RIFF\x00\x00\x00\x00WEBPVP8 not-really-decodable-but-huge")
	out, mime := FitImage(data, "image/webp")

	if !bytes.Equal(out, data) || mime != "image/webp" {
		t.Error("an undecodable image must pass through unchanged")
	}
}
