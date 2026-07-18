// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// TestCompositor_CursorShownAfterStartHide is the regression test for the
// "no cursor on the input line" bug. TUI.Start() hides the hardware cursor
// (terminal.HideCursor), so the Compositor must NOT assume it starts visible:
// the first frame that carries an input cursor must emit \x1b[?25h (show).
// A regression that initializes cursorVisible=true leaves the cursor hidden
// for the whole session.
func TestCompositor_CursorShownAfterStartHide(t *testing.T) {
	term := &fakeTerminal{w: 40, h: 10}
	comp := NewCompositor(term)
	comp.InitialClear()

	// The editor is focused and emits a cursor: Scene.Cursor is set.
	scene := &Scene{
		TerminalW: 40, TerminalH: 10,
		Cursor: &CursorPos{Row: 8, Col: 3},
		Layers: []Layer{
			{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 40, H: 8}, Content: []string{"a", "b", "c"}},
			{Name: "editor", Kind: LayerBase, Rect: Rect{X: 0, Y: 8, W: 40, H: 2}, Content: []string{"> hello", ""}},
		},
	}
	comp.Render(scene)

	if !strings.Contains(strings.Join(term.writes, ""), "\x1b[?25h") {
		t.Errorf("first cursor frame did not emit show-cursor (\\x1b[?25h); writes:\n%s",
			joinEscaped(term.writes))
	}
}

// TestCompositor_CursorPositionedOnEditorRow verifies the hardware cursor CUP
// lands on the editor's screen row, not on a transcript row.
func TestCompositor_CursorPositionedOnEditorRow(t *testing.T) {
	term := &fakeTerminal{w: 40, h: 10}
	comp := NewCompositor(term)
	comp.InitialClear()
	scene := &Scene{
		TerminalW: 40, TerminalH: 10,
		Cursor: &CursorPos{Row: 8, Col: 3},
		Layers: []Layer{
			{Name: "editor", Kind: LayerBase, Rect: Rect{X: 0, Y: 8, W: 40, H: 2}, Content: []string{"> hello", ""}},
		},
	}
	comp.Render(scene)

	emu := NewTermEmulator(10, 40)
	for _, w := range term.writes {
		emu.Process(w)
	}
	// Canvas row 8 -> screen row 9 (1-indexed). Col 3 -> 1-indexed col 4.
	if emu.row != 8 {
		t.Errorf("hardware cursor row = %d, want 8 (screen row 9)", emu.row)
	}
	if emu.col != 3 {
		t.Errorf("hardware cursor col = %d, want 3", emu.col)
	}
}

// joinEscaped flattens writes for assertion messages.
func joinEscaped(writes []string) string {
	var b strings.Builder
	for i, w := range writes {
		b.WriteString("  [")
		b.WriteString(itoaStr(i))
		b.WriteString("] ")
		b.WriteString(truncEscape(w))
		b.WriteString("\n")
	}
	return b.String()
}
