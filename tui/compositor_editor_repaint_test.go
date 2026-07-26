// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// TestCompositor_ScrollingFrameRepaintsChangedChromeRow reproduces the bug
// where a submitted command stays visible in the editor until the next
// keystroke. On the submit frame the transcript grows (the command echo), so
// advanceScrollback runs and lastScrollCount > 0. repaintWindow then skips the
// bottom lastScrollCount CANVAS rows — but those are the editor/chrome rows,
// not the transcript rows the scroll actually wrote. The just-cleared editor
// line is therefore never repainted and the terminal keeps showing the stale
// command. After the fix, a chrome row that changed in the same frame must be
// repainted even when the frame scrolled.
func TestCompositor_ScrollingFrameRepaintsChangedChromeRow(t *testing.T) {
	const (
		termH  = 10
		w      = 40
		chrome = 2 // editor border + input line
	)
	term := &fakeTerminal{w: w, h: termH}
	comp := NewCompositor(term)

	mkScene := func(history []string, editorLine string) *Scene {
		content := append([]string{}, history...)
		content = append(content, "EDIT-BORDER", editorLine)
		return &Scene{TerminalW: w, TerminalH: termH, ChromeHeight: chrome,
			Layers: []Layer{{Name: "c", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: w, H: len(content)}, Content: content}}}
	}

	history := []string{}
	// Grow the transcript until it scrolls steadily with chrome pinned.
	for i := 0; i < 12; i++ {
		history = append(history, "row-"+itoaStr(i))
		comp.Render(mkScene(history, "> /tools"))
	}

	// Submit frame: the command echoes into the transcript (one more row, so
	// the frame scrolls) AND the editor line is cleared in the same frame.
	history = append(history, "/tools")
	comp.Render(mkScene(history, "> "))

	emu := NewTermEmulator(termH, w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}

	// The bottom chrome rows are the editor border + input line. The input
	// line must show the cleared editor, not the stale "/tools".
	bottom := emu.Visible(termH - 1)
	if strings.Contains(bottom, "/tools") {
		t.Fatalf("stale submitted command still visible on editor row after submit frame.\nrow: %q", bottom)
	}
	if !strings.Contains(bottom, ">") {
		t.Fatalf("expected cleared editor prompt on bottom chrome row, got %q", bottom)
	}
}
