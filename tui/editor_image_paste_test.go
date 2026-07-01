// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"image"
	"testing"
)

func TestEditor_ImagePaste_InsertsReference(t *testing.T) {
	e := NewEditor()
	e.readClipboardImage = func() (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
	}
	// Override save to avoid temp file creation in tests.
	oldSave := saveClipboardImage
	saveClipboardImage = func(img image.Image) (string, error) {
		return "/tmp/test-paste.png", nil
	}
	defer func() { saveClipboardImage = oldSave }()

	// handlePaste is an internal method that runs on the commandLoop; in this
	// single-goroutine test it is called directly.
	e.handlePaste("some text")

	text := e.Text()
	if text != "/tmp/test-paste.png" {
		t.Errorf("text = %q, want /tmp/test-paste.png", text)
	}
}

func TestEditor_ImagePaste_CallsOnImagePaste(t *testing.T) {
	e := NewEditor()
	e.readClipboardImage = func() (image.Image, error) {
		return image.NewRGBA(image.Rect(0, 0, 1, 1)), nil
	}
	oldSave := saveClipboardImage
	saveClipboardImage = func(img image.Image) (string, error) {
		return "/tmp/test-paste.png", nil
	}
	defer func() { saveClipboardImage = oldSave }()

	var gotPath string
	e.OnImagePaste = func(path string) {
		gotPath = path
	}

	// Drive paste through the public HandleInput entry so the lock discipline
	// (queue callback under mu, run after release) is exercised exactly as in
	// production. "some image\n" is treated as a paste event.
	e.SetFocused(true)
	e.HandleInput("some image\n")

	if gotPath != "/tmp/test-paste.png" {
		t.Errorf("OnImagePaste path = %q, want /tmp/test-paste.png", gotPath)
	}
	if e.Text() != "" {
		t.Errorf("editor should remain empty when OnImagePaste is set, got %q", e.Text())
	}
}

func TestEditor_TextPaste_FallbackWhenNoImage(t *testing.T) {
	e := NewEditor()
	e.readClipboardImage = func() (image.Image, error) {
		return nil, nil
	}

	e.handlePaste("pasted text")

	if e.Text() != "pasted text" {
		t.Errorf("text = %q, want pasted text", e.Text())
	}
}
