// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"bytes"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/internal"
)

// clipboardImageBytes returns real PNG bytes: the payload a platform backend
// hands over when the clipboard holds an image.
func clipboardImageBytes(t *testing.T) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, 2, 2))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// imageOnlyClipboard returns an editor whose clipboard holds image bytes and
// nothing else, with the production store left in place: a paste on it runs the
// real "decode → store → path" chain, exactly as the terminal does.
func imageOnlyClipboard(t *testing.T) *Editor {
	t.Helper()
	payload := clipboardImageBytes(t)
	e := NewEditor()
	e.readClipboardImage = func() (image.Image, error) {
		img, _, err := image.Decode(bytes.NewReader(payload))
		return img, err
	}
	e.readClipboardFilePaths = func() []string { return nil }
	e.readClipboardText = func() (string, bool) { return "", false }
	return e
}

// isolateImageStore points the durable image store at a temp dir so a paste test
// never writes into the developer's real cache.
func isolateImageStore(t *testing.T) string {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)                                   // darwin: $HOME/Library/Caches
	t.Setenv("XDG_CACHE_HOME", filepath.Join(home, "cache")) // linux
	t.Setenv("LocalAppData", filepath.Join(home, "local"))   // windows
	dir, err := internal.ImageStoreDir()
	if err != nil {
		t.Fatalf("ImageStoreDir: %v", err)
	}
	return dir
}

// TestEditor_PasteFromClipboard_StoresImageAndInsertsStoredPath is the whole
// terminal chain for B6: Ctrl+V on a clipboard holding image bytes stores the
// image through the real store and leaves the stored path in the input line, so
// the agent receives it as an attachment (internal.IsImageFile is the submit
// path's own attachment predicate).
func TestEditor_PasteFromClipboard_StoresImageAndInsertsStoredPath(t *testing.T) {
	store := isolateImageStore(t)
	e := imageOnlyClipboard(t)

	e.SetFocused(true)
	e.HandleInput(KeyCtrlV)

	got := e.Text()
	if got == "" {
		t.Fatal("Ctrl+V on a clipboard image inserted nothing")
	}
	if filepath.Dir(got) != store {
		t.Errorf("inserted %q, want a path inside the image store %q", got, store)
	}
	if !internal.IsImageFile(got) {
		t.Errorf("IsImageFile(%q) = false: the inserted path is not an attachment", got)
	}
	if entries, err := os.ReadDir(store); err != nil || len(entries) != 1 {
		t.Errorf("image store holds %d files (err=%v), want the one pasted image", len(entries), err)
	}
}

// TestEditor_PasteFromClipboard_NoImageOnClipboardInsertsNothing: a clipboard
// with no image and no text must leave the input line exactly as it was.
func TestEditor_PasteFromClipboard_NoImageOnClipboardInsertsNothing(t *testing.T) {
	store := isolateImageStore(t)
	e := NewEditor()
	e.readClipboardImage = func() (image.Image, error) { return nil, nil }
	e.readClipboardFilePaths = func() []string { return nil }
	e.readClipboardText = func() (string, bool) { return "", false }
	e.SetText("keep me")

	e.SetFocused(true)
	e.HandleInput(KeyCtrlV)

	if got := e.Text(); got != "keep me" {
		t.Errorf("text = %q, want the untouched %q", got, "keep me")
	}
	if entries, _ := os.ReadDir(store); len(entries) != 0 {
		t.Errorf("image store holds %d files, want none", len(entries))
	}
}

// TestEditor_PasteFromClipboard_ReaderUnavailableInsertsNothing: on a platform
// with no clipboard backend every reader is nil. The paste must be a quiet
// no-op — no panic, no store write.
func TestEditor_PasteFromClipboard_ReaderUnavailableInsertsNothing(t *testing.T) {
	store := isolateImageStore(t)
	e := NewEditor()
	e.readClipboardImage = nil
	e.readClipboardFilePaths = nil
	e.readClipboardText = nil
	e.SetText("keep me")

	e.SetFocused(true)
	e.HandleInput(KeyCtrlV)

	if got := e.Text(); got != "keep me" {
		t.Errorf("text = %q, want the untouched %q", got, "keep me")
	}
	if entries, _ := os.ReadDir(store); len(entries) != 0 {
		t.Errorf("image store holds %d files, want none", len(entries))
	}
}
