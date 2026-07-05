// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

// TestEditor_CursorUp_FromEmptySecondLine verifies that pressing Up from an
// empty second logical line moves the cursor to the first line.
func TestEditor_CursorUp_FromEmptySecondLine(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetText("abc")
	ed.lastWidth = 80
	ed.HandleInput("alt+enter")

	if got, want := ed.Text(), "abc\n"; got != want {
		t.Fatalf("expected newline after alt+enter, got %q", got)
	}
	if got, want := ed.pos, 4; got != want {
		t.Fatalf("pos after alt+enter = %d, want 4", got)
	}

	ed.HandleInput(KeyUp)
	if got, want := ed.pos, 0; got != want {
		t.Errorf("pos after Up = %d, want 0", got)
	}
}
