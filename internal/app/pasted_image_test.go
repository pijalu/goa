// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"path/filepath"
	"testing"
)

// TestPastedImagePathLandsInTheInputLine closes the terminal image-paste chain
// at the app layer: the editor's paste resolves the clipboard, stores the image
// and hands the stored path to OnImagePaste, which must insert it into the input
// line (separated from whatever the cursor sits against), so the submit path's
// own attachment predicate (internal.IsImageFile) picks it up.
func TestPastedImagePathLandsInTheInputLine(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	sc.app.subs.inputEditor = sc.editor

	sc.editor.SetText("look at") // cursor ends up after the text
	path := filepath.Join(t.TempDir(), "goa-image-1234.png")
	sc.engine.ApplySync(func() {
		sc.app.handlePastedImage(sc.engine, sc.chat, path)
	})

	// The inserted token must not fuse with the text before it.
	if want, got := "look at "+path, sc.editor.Text(); got != want {
		t.Errorf("input line = %q, want %q", got, want)
	}
}
