// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package internal

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestPreferredImageMime(t *testing.T) {
	tests := []struct {
		name  string
		types []string
		want  string
	}{
		{"prefers png over jpeg", []string{"image/jpeg", "image/png"}, "image/png"},
		{"jpeg when no png", []string{"text/plain", "image/jpeg"}, "image/jpeg"},
		{"jpeg before webp", []string{"image/webp", "image/jpeg"}, "image/jpeg"},
		{"webp before gif", []string{"image/gif", "image/webp"}, "image/webp"},
		{"skips charset parameter", []string{"image/png;charset=binary"}, "image/png"},
		{"case insensitive", []string{"IMAGE/JPEG"}, "image/jpeg"},
		{"blank entries ignored", []string{"", "  ", "image/gif"}, "image/gif"},
		// A format the store cannot keep is not a flavour to select: reading it
		// would end in a paste that silently does nothing (see SniffImageExt).
		{"unsupported format is not selected", []string{"image/bmp"}, ""},
		{"tiff is not selected", []string{"image/tiff", "image/bmp"}, ""},
		{"no image", []string{"text/plain", "text/html"}, ""},
		{"empty", nil, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := preferredImageMime(tc.types); got != tc.want {
				t.Errorf("preferredImageMime(%v) = %q, want %q", tc.types, got, tc.want)
			}
		})
	}
}

func TestSniffImageExt(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		want string
	}{
		{"png", []byte("\x89PNG\r\n\x1a\n"), ".png"},
		{"jpeg", []byte("\xff\xd8\xff"), ".jpg"},
		{"gif", []byte("GIF89a"), ".gif"},
		{"webp", []byte("RIFF\x00\x00\x00\x00WEBP"), ".webp"},
		{"text", []byte("hello"), ""},
		{"too short for webp", []byte("RIFFWEBP"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SniffImageExt(tc.data); got != tc.want {
				t.Errorf("SniffImageExt(%q) = %q, want %q", tc.data, got, tc.want)
			}
		})
	}
}

// pngFile writes a tiny PNG into dir and returns its path.
func pngFile(t *testing.T, dir, name string) string {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 1, 1))); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, buf.Bytes(), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestIsImageFile is the single predicate behind "this token is an attachment":
// the bytes decide, not the extension.
func TestIsImageFile(t *testing.T) {
	dir := t.TempDir()
	real := pngFile(t, dir, "real.png")
	fake := filepath.Join(dir, "fake.png")
	if err := os.WriteFile(fake, []byte("not an image at all"), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		path string
		want bool
	}{
		{"real image", real, true},
		{"wrong extension, real bytes", pngFile(t, dir, "odd.bin"), true},
		{"image extension, wrong bytes", fake, false},
		{"missing", filepath.Join(dir, "nope.png"), false},
		{"directory", dir, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsImageFile(tc.path); got != tc.want {
				t.Errorf("IsImageFile(%q) = %v, want %v", tc.path, got, tc.want)
			}
		})
	}
}

func TestExistingRegularFiles(t *testing.T) {
	dir := t.TempDir()
	file := pngFile(t, dir, "a.png")

	got := existingRegularFiles([]string{"", "   ", file, dir, filepath.Join(dir, "missing")})
	if len(got) != 1 || got[0] != file {
		t.Errorf("existingRegularFiles = %v, want [%s]", got, file)
	}
}

// TestNewImageFile_LandsInStore pins the durable location: the path is embedded
// in conversation history and re-read on later requests, so it must not be an
// os.TempDir path that a reboot or a cleaner can delete.
func TestNewImageFile_LandsInStore(t *testing.T) {
	dir, err := ImageStoreDir()
	if err != nil {
		t.Fatal(err)
	}

	f, err := NewImageFile(".png")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		f.Close()
		os.Remove(f.Name())
	}()

	if filepath.Dir(f.Name()) != dir {
		t.Errorf("image file dir = %q, want %q", filepath.Dir(f.Name()), dir)
	}
	if filepath.Ext(f.Name()) != ".png" {
		t.Errorf("ext = %q, want .png", filepath.Ext(f.Name()))
	}
}

// TestPruneImages_RemovesOnlyExpired keeps a fresh attachment (still referenced
// by an active session) while reaping an old one.
func TestPruneImages_RemovesOnlyExpired(t *testing.T) {
	dir, err := ImageStoreDir()
	if err != nil {
		t.Fatal(err)
	}
	fresh, err := NewImageFile(".png")
	if err != nil {
		t.Fatal(err)
	}
	fresh.Close()
	defer os.Remove(fresh.Name())

	stale, err := NewImageFile(".png")
	if err != nil {
		t.Fatal(err)
	}
	stale.Close()
	defer os.Remove(stale.Name())

	old := time.Now().Add(-2 * ImageStoreLifetime)
	if err := os.Chtimes(stale.Name(), old, old); err != nil {
		t.Fatal(err)
	}

	PruneImages()

	if _, err := os.Stat(stale.Name()); !os.IsNotExist(err) {
		t.Error("expired image was not pruned")
	}
	if _, err := os.Stat(fresh.Name()); err != nil {
		t.Errorf("fresh image was pruned: %v", err)
	}
	if filepath.Dir(fresh.Name()) != dir {
		t.Errorf("pruned store mismatch: %q", dir)
	}
}
