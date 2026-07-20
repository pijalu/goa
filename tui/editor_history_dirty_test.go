// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

// TestEditor_UpArrow_DirtyInput_NoRecall verifies that pressing Up with typed
// (unsent) content does NOT recall history — the in-progress text must never
// be clobbered (bugs.md: history recall only on non-dirty input).
func TestEditor_UpArrow_DirtyInput_NoRecall(t *testing.T) {
	ed := NewEditor()
	ed.SetHistory([]string{"old-a", "old-b"})
	ed.SetFocused(true)

	ed.HandleInput("h")
	ed.HandleInput("i")
	if ed.Text() != "hi" {
		t.Fatalf("setup: Text() = %q, want %q", ed.Text(), "hi")
	}

	ed.HandleInput(KeyUp)
	if got := ed.Text(); got != "hi" {
		t.Errorf("Up on dirty input: Text() = %q, want %q (no history recall)", got, "hi")
	}
	if ed.histIdx != -1 {
		t.Errorf("Up on dirty input: histIdx = %d, want -1 (not browsing)", ed.histIdx)
	}
}

// TestEditor_UpArrow_EditedHistoryEntry_NoNavigate verifies that editing a
// recalled history entry makes the line dirty, so further Up does not
// navigate away and lose the edit.
func TestEditor_UpArrow_EditedHistoryEntry_NoNavigate(t *testing.T) {
	ed := NewEditor()
	ed.SetHistory([]string{"aaa", "bbb"})
	ed.SetFocused(true)

	ed.HandleInput(KeyUp) // recalls "bbb" (pristine — not dirty)
	if got := ed.Text(); got != "bbb" {
		t.Fatalf("setup: Text() = %q, want %q", got, "bbb")
	}
	ed.HandleInput("!") // edit the recalled entry → dirty

	ed.HandleInput(KeyUp)
	if got := ed.Text(); got != "bbb!" {
		t.Errorf("Up after editing recalled entry: Text() = %q, want %q (no navigate)", got, "bbb!")
	}
}

// TestEditor_UpArrow_AfterDeleteToEmpty_Recalls verifies that when the user
// deletes the buffer back to empty, the line is no longer dirty and Up recalls
// history again.
func TestEditor_UpArrow_AfterDeleteToEmpty_Recalls(t *testing.T) {
	ed := NewEditor()
	ed.SetHistory([]string{"xyz"})
	ed.SetFocused(true)

	ed.HandleInput("q")
	ed.HandleInput(KeyBackspace) // buffer back to empty
	if ed.Text() != "" {
		t.Fatalf("setup: Text() = %q, want empty", ed.Text())
	}

	ed.HandleInput(KeyUp)
	if got := ed.Text(); got != "xyz" {
		t.Errorf("Up after delete-to-empty: Text() = %q, want %q", got, "xyz")
	}
}

// TestEditor_UpArrow_AfterSubmit_Recalls verifies submitting clears dirty, so
// Up recalls history on the fresh empty line.
func TestEditor_UpArrow_AfterSubmit_Recalls(t *testing.T) {
	ed := NewEditor()
	ed.SetHistory([]string{"prev"})
	ed.SetFocused(true)

	ed.HandleInput("x")
	ed.HandleInput(KeyEnter) // submit → buffer cleared, dirty cleared, "x" in history

	ed.HandleInput(KeyUp)
	if got := ed.Text(); got != "x" {
		t.Errorf("Up after submit: Text() = %q, want %q (newest entry)", got, "x")
	}
	ed.HandleInput(KeyUp)
	if got := ed.Text(); got != "prev" {
		t.Errorf("second Up after submit: Text() = %q, want %q", got, "prev")
	}
}

// TestEditor_DownArrow_DirtyInput_NoNavigate verifies Down does not browse
// history when the line is dirty.
func TestEditor_DownArrow_DirtyInput_NoNavigate(t *testing.T) {
	ed := NewEditor()
	ed.SetHistory([]string{"one", "two"})
	ed.SetFocused(true)

	ed.HandleInput("z")
	ed.HandleInput(KeyDown)
	if got := ed.Text(); got != "z" {
		t.Errorf("Down on dirty input: Text() = %q, want %q", got, "z")
	}
	if ed.histIdx != -1 {
		t.Errorf("Down on dirty input: histIdx = %d, want -1", ed.histIdx)
	}
}
