// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

// TestFindVisualLine_WrapBoundary verifies that the position at the exact
// start of a wrapped visual line is mapped to that visual line, not the
// previous one.
func TestFindVisualLine_WrapBoundary(t *testing.T) {
	// "1234567890" is exactly 10 display columns, so "abc" starts on the
	// second visual line at buffer position 10.
	vlm := buildVisualLineMap("1234567890abc", 10)
	if len(vlm) != 2 {
		t.Fatalf("expected 2 visual lines, got %d: %+v", len(vlm), vlm)
	}
	if got := findVisualLine("1234567890abc", vlm, 10); got != 1 {
		t.Errorf("findVisualLine(pos=10) = %d, want 1", got)
	}
}

// TestFindVisualLine_LogicalLineEnd verifies that the position right after
// the last character of a logical line still belongs to that line.
func TestFindVisualLine_LogicalLineEnd(t *testing.T) {
	vlm := buildVisualLineMap("hello\nworld", 80)
	if len(vlm) != 2 {
		t.Fatalf("expected 2 visual lines, got %d", len(vlm))
	}
	if got := findVisualLine("hello\nworld", vlm, 5); got != 0 {
		t.Errorf("findVisualLine(pos=5) = %d, want 0", got)
	}
}

// TestEditor_CursorUp_FromSecondVisualLine verifies that Up moves from the
// second visual line (after a wrap) to the first visual line.
func TestEditor_CursorUp_FromSecondVisualLine(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetText("1234567890abc")
	ed.pos = 10 // cursor at 'a' on second visual line
	ed.lastWidth = 10

	ed.HandleInput(KeyUp)
	if got, want := ed.pos, 0; got != want {
		t.Errorf("pos after Up = %d, want %d", got, want)
	}
}

// TestEditor_CursorUp_FromSecondLine verifies that Up moves from the second
// logical line to the first logical line.
func TestEditor_CursorUp_FromSecondLine(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetText("line1\nsummarize")
	ed.pos = 6 // cursor at 's' of "summarize"
	ed.lastWidth = 80

	ed.HandleInput(KeyUp)
	if got, want := ed.pos, 0; got != want {
		t.Errorf("pos after Up = %d, want %d", got, want)
	}
}

// TestEditor_VisualCursor_WrapBoundary verifies VisualCursor reports the
// correct visual line when the cursor is at the start of a wrapped visual line.
func TestEditor_VisualCursor_WrapBoundary(t *testing.T) {
	ed := NewEditor()
	ed.SetText("1234567890abc")
	ed.pos = 10

	line, col := ed.VisualCursor(10)
	if line != 1 {
		t.Errorf("VisualCursor line = %d, want 1", line)
	}
	if col != 0 {
		t.Errorf("VisualCursor col = %d, want 0", col)
	}
}
