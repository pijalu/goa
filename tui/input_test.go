// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"
)

// TestInput_HistoryUp_EmptyBuffer_RecallsLast verifies that pressing Up on an
// empty single-line input recalls the most recent history entry.
func TestInput_HistoryUp_EmptyBuffer_RecallsLast(t *testing.T) {
	in := NewInput()
	in.SetFocused(true)
	in.history = []string{"a", "b"}

	in.HandleInput(KeyUp)
	if got, want := in.Text(), "b"; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
	if got, want := in.histIdx, 1; got != want {
		t.Errorf("histIdx = %d, want %d", got, want)
	}
}

// TestInput_HistoryUp_ToTop verifies that pressing Up continues to older
// entries and stops at the top of history.
func TestInput_HistoryUp_ToTop(t *testing.T) {
	in := NewInput()
	in.SetFocused(true)
	in.history = []string{"a", "b"}

	in.HandleInput(KeyUp)
	in.HandleInput(KeyUp)
	if got, want := in.Text(), "a"; got != want {
		t.Errorf("Text() after two Up = %q, want %q", got, want)
	}
	if got, want := in.histIdx, 0; got != want {
		t.Errorf("histIdx = %d, want %d", got, want)
	}

	in.HandleInput(KeyUp)
	if got, want := in.Text(), "a"; got != want {
		t.Errorf("Text() at top should stay %q, got %q", want, got)
	}
	if got, want := in.histIdx, 0; got != want {
		t.Errorf("histIdx = %d, want %d", got, want)
	}
}

// TestInput_HistoryDown_PastNewest_ReturnsToEmptyLine verifies that pressing
// Down from the newest history entry returns to an empty line with the cursor
// at column 0 and history reset to editing mode.
func TestInput_HistoryDown_PastNewest_ReturnsToEmptyLine(t *testing.T) {
	in := NewInput()
	in.SetFocused(true)
	in.history = []string{"a", "b"}

	in.HandleInput(KeyUp)
	in.HandleInput(KeyUp) // now at "a"
	in.HandleInput(KeyDown) // to "b"
	in.HandleInput(KeyDown) // past newest -> empty

	if got, want := in.Text(), ""; got != want {
		t.Errorf("Text() = %q, want empty", got)
	}
	if got, want := in.editor.Cursor(), 0; got != want {
		t.Errorf("cursor = %d, want 0", got)
	}
	if got, want := in.histIdx, -1; got != want {
		t.Errorf("histIdx = %d, want -1", got)
	}
}
