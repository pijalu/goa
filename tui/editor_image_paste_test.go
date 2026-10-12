// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"
)

// testClipboardImage is the image payload the paste tests hand back: real PNG
// bytes, because the editor stores what the clipboard published rather than
// re-encoding a decoded image.
func testClipboardImage(t *testing.T) []byte {
	t.Helper()
	return clipboardImageBytes(t)
}

// newPasteEditor returns an editor whose clipboard readers are all stubbed so a
// test never touches the developer's real clipboard. image is the clipboard's
// image bytes, or nil for "no image on the clipboard".
func newPasteEditor(t *testing.T, image []byte, paths []string, text string, textOK bool) *Editor {
	t.Helper()
	e := NewEditor()
	e.readClipboardImage = func() ([]byte, bool) { return image, image != nil }
	e.readClipboardFilePaths = func() []string { return paths }
	e.readClipboardText = func() (string, bool) { return text, textOK }
	return e
}

// TestEditor_PasteFromClipboard_InsertsImageReference covers the image branch of
// the explicit paste: with no OnImagePaste hook the path is inserted as text so
// the agent receives it as an attachment.
func TestEditor_PasteFromClipboard_InsertsImageReference(t *testing.T) {
	e := newPasteEditor(t, testClipboardImage(t), nil, "", false)
	oldSave := saveClipboardImage
	saveClipboardImage = func([]byte) (string, error) { return "/tmp/test-paste.png", nil }
	defer func() { saveClipboardImage = oldSave }()

	e.pasteFromClipboard()

	if got := e.Text(); got != "/tmp/test-paste.png" {
		t.Errorf("text = %q, want /tmp/test-paste.png", got)
	}
}

// TestEditor_PasteFromClipboard_CallsOnImagePaste drives the paste through the
// public input entry so the ctrl+v binding and the callback batching are
// exercised exactly as in production.
func TestEditor_PasteFromClipboard_CallsOnImagePaste(t *testing.T) {
	e := newPasteEditor(t, testClipboardImage(t), nil, "", false)
	oldSave := saveClipboardImage
	saveClipboardImage = func([]byte) (string, error) { return "/tmp/test-paste.png", nil }
	defer func() { saveClipboardImage = oldSave }()

	var gotPath string
	e.OnImagePaste = func(path string) { gotPath = path }

	e.SetFocused(true)
	e.HandleInput(KeyCtrlV)

	if gotPath != "/tmp/test-paste.png" {
		t.Errorf("OnImagePaste path = %q, want /tmp/test-paste.png", gotPath)
	}
	if e.Text() != "" {
		t.Errorf("editor should remain empty when OnImagePaste is set, got %q", e.Text())
	}
}

// TestEditor_PasteFromClipboard_RawByteBinding proves the raw ctrl+v byte
// reaches the same handler (terminals that do not report modified keys).
func TestEditor_PasteFromClipboard_RawByteBinding(t *testing.T) {
	keys := decodeKeys([]byte{0x16})
	if len(keys) != 1 || keys[0] != KeyCtrlV {
		t.Fatalf("decodeKeys(0x16) = %v, want [%s]", keys, KeyCtrlV)
	}
}

// TestEditor_PasteFromClipboard_TextFallback covers the final branch: no file
// list, no image, plain text.
func TestEditor_PasteFromClipboard_TextFallback(t *testing.T) {
	e := newPasteEditor(t, nil, nil, "hello from clipboard", true)

	e.SetFocused(true)
	e.HandleInput(KeyCtrlV)

	if got := e.Text(); got != "hello from clipboard" {
		t.Errorf("text = %q, want %q", got, "hello from clipboard")
	}
}

// TestEditor_PasteFromClipboard_FilePathsWinOverImage pins the precedence
// (paths → image → text) that a Finder/Explorer copy depends on: copying a file
// often publishes both a file list and image data.
func TestEditor_PasteFromClipboard_FilePathsWinOverImage(t *testing.T) {
	e := newPasteEditor(t, testClipboardImage(t), []string{"/a/one.png", "/b/two.txt"}, "", false)

	e.pasteFromClipboard()

	if got := e.Text(); got != "/a/one.png /b/two.txt" {
		t.Errorf("text = %q, want the file paths", got)
	}
}

// TestEditor_PasteFromClipboard_EmptyClipboard leaves the buffer untouched.
func TestEditor_PasteFromClipboard_EmptyClipboard(t *testing.T) {
	e := newPasteEditor(t, nil, nil, "", false)

	e.SetFocused(true)
	e.HandleInput(KeyCtrlV)

	if got := e.Text(); got != "" {
		t.Errorf("text = %q, want empty", got)
	}
}

// TestEditor_TextPaste_KeepsTextWhenClipboardHasImage is the regression test for
// the clipboard-image hijack: a pasted text blob must never be replaced by a
// clipboard image that happens to be present (the clipboard can hold both).
func TestEditor_TextPaste_KeepsTextWhenClipboardHasImage(t *testing.T) {
	e := newPasteEditor(t, testClipboardImage(t), nil, "clipboard text", true)
	oldSave := saveClipboardImage
	saveClipboardImage = func([]byte) (string, error) { return "/tmp/test-paste.png", nil }
	defer func() { saveClipboardImage = oldSave }()

	// A text paste arrives as a single multi-line event, as in production.
	e.SetFocused(true)
	e.HandleInput("line one\nline two\n")

	if got := e.Text(); got != "line one\nline two\n" {
		t.Errorf("text = %q, want the pasted text (image must not hijack it)", got)
	}
}

// TestEditor_TextPaste_FallbackWhenNoImage keeps the simple text-paste path
// covered.
func TestEditor_TextPaste_FallbackWhenNoImage(t *testing.T) {
	e := newPasteEditor(t, nil, nil, "", false)

	e.handlePaste("pasted text")

	if e.Text() != "pasted text" {
		t.Errorf("text = %q, want pasted text", e.Text())
	}
}
