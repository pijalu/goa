// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"
)

// TestEditor_HistoryNavigation_Order verifies that pressing Up recalls the
// most recently submitted entry first, and Down moves back toward newer
// entries. This matches the bug requirement that "/<command>" history follows
// entry order.
func TestEditor_HistoryNavigation_Order(t *testing.T) {
	ed := NewEditor()
	ed.SetHistory([]string{"first", "second", "third"})

	// From editing state, Up should recall the newest entry (third).
	ed.navigateHistory(-1)
	if got, want := ed.Text(), "third"; got != want {
		t.Errorf("first Up: got %q, want %q", got, want)
	}

	// Up again should recall the second-newest entry (second).
	ed.navigateHistory(-1)
	if got, want := ed.Text(), "second"; got != want {
		t.Errorf("second Up: got %q, want %q", got, want)
	}

	// Down should move back toward newer entries (third).
	ed.navigateHistory(1)
	if got, want := ed.Text(), "third"; got != want {
		t.Errorf("Down after second: got %q, want %q", got, want)
	}

	// Down from newest should return to editing state (empty).
	ed.navigateHistory(1)
	if got, want := ed.Text(), ""; got != want {
		t.Errorf("Down from newest: got %q, want %q", got, want)
	}
}

// TestEditor_HistoryNavigation_SlashCommands verifies that slash commands are
// added to history in submission order and can be recalled with Up.
func TestEditor_HistoryNavigation_SlashCommands(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	var submitted []string
	ed.SetOnSubmit(func(text string) {
		submitted = append(submitted, text)
	})

	ed.SetText("/model")
	ed.HandleInput(KeyEnter)

	ed.SetText("/goal:new test")
	ed.HandleInput(KeyEnter)

	ed.SetText("/config")
	ed.HandleInput(KeyEnter)

	want := []string{"/model", "/goal:new test", "/config"}
	if len(submitted) != len(want) {
		t.Fatalf("submitted = %v, want %v", submitted, want)
	}
	for i := range want {
		if submitted[i] != want[i] {
			t.Errorf("submitted[%d] = %q, want %q", i, submitted[i], want[i])
		}
	}

	// Up from editing should recall the last command first.
	ed.navigateHistory(-1)
	if got := ed.Text(); got != "/config" {
		t.Errorf("Up after slash commands: got %q, want /config", got)
	}
	ed.navigateHistory(-1)
	if got := ed.Text(); got != "/goal:new test" {
		t.Errorf("second Up: got %q, want /goal:new test", got)
	}
	ed.navigateHistory(-1)
	if got := ed.Text(); got != "/model" {
		t.Errorf("third Up: got %q, want /model", got)
	}
}

// TestEditor_UpArrow_EmptyBuffer_RecallsLast verifies that pressing Up on an
// empty editor recalls the most recent history entry.
func TestEditor_UpArrow_EmptyBuffer_RecallsLast(t *testing.T) {
	ed := NewEditor()
	ed.SetHistory([]string{"a", "b"})
	ed.SetFocused(true)

	ed.HandleInput(KeyUp)
	if got, want := ed.Text(), "b"; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
	if got, want := ed.histIdx, 1; got != want {
		t.Errorf("histIdx = %d, want %d", got, want)
	}
}

// TestEditor_UpArrow_ToTop verifies that pressing Up continues to older entries
// and stops at the top of history.
func TestEditor_UpArrow_ToTop(t *testing.T) {
	ed := NewEditor()
	ed.SetHistory([]string{"a", "b"})
	ed.SetFocused(true)

	ed.HandleInput(KeyUp)
	ed.HandleInput(KeyUp)
	if got, want := ed.Text(), "a"; got != want {
		t.Errorf("Text() after two Up = %q, want %q", got, want)
	}
	if got, want := ed.histIdx, 0; got != want {
		t.Errorf("histIdx = %d, want %d", got, want)
	}

	ed.HandleInput(KeyUp)
	if got, want := ed.Text(), "a"; got != want {
		t.Errorf("Text() at top should stay %q, got %q", want, got)
	}
	if got, want := ed.histIdx, 0; got != want {
		t.Errorf("histIdx = %d, want %d", got, want)
	}
}

// TestEditor_DownArrow_PastNewest_ReturnsToEmptyLine verifies that pressing Down
// from the newest history entry returns to an empty editable line with the
// cursor at column 0.
func TestEditor_DownArrow_PastNewest_ReturnsToEmptyLine(t *testing.T) {
	ed := NewEditor()
	ed.SetHistory([]string{"a", "b"})
	ed.SetFocused(true)

	ed.HandleInput(KeyUp)
	ed.HandleInput(KeyUp) // now at "a"
	ed.HandleInput(KeyDown) // to "b"
	ed.HandleInput(KeyDown) // past newest -> empty

	if got, want := ed.Text(), ""; got != want {
		t.Errorf("Text() = %q, want empty", got)
	}
	if got, want := ed.pos, 0; got != want {
		t.Errorf("pos = %d, want 0", got)
	}
	if got, want := ed.histIdx, -1; got != want {
		t.Errorf("histIdx = %d, want -1", got)
	}
}

// TestEditor_DownArrow_FromMultilineHistory_EmptyReachable verifies that Down
// can return to an empty line even after navigating through a multiline entry.
func TestEditor_DownArrow_FromMultilineHistory_EmptyReachable(t *testing.T) {
	ed := NewEditor()
	ed.SetHistory([]string{"line1\nline2", "x"})
	ed.SetFocused(true)

	ed.HandleInput(KeyUp) // "x"
	ed.HandleInput(KeyUp) // "line1\nline2"
	ed.HandleInput(KeyDown) // "x"
	ed.HandleInput(KeyDown) // empty

	if got, want := ed.Text(), ""; got != want {
		t.Errorf("Text() = %q, want empty", got)
	}
	if got, want := ed.pos, 0; got != want {
		t.Errorf("pos = %d, want 0", got)
	}
	if got, want := ed.histIdx, -1; got != want {
		t.Errorf("histIdx = %d, want -1", got)
	}
}

// TestEditor_HistoryNavigation_DraftRestore verifies that the current editing
// draft is preserved when browsing history and restored when returning to
// editing state.
func TestEditor_HistoryNavigation_DraftRestore(t *testing.T) {
	ed := NewEditor()
	ed.SetHistory([]string{"old"})
	ed.SetText("draft")

	ed.navigateHistory(-1)
	if got, want := ed.Text(), "old"; got != want {
		t.Errorf("Up: got %q, want %q", got, want)
	}

	ed.navigateHistory(1)
	if got, want := ed.Text(), "draft"; got != want {
		t.Errorf("Down restore draft: got %q, want %q", got, want)
	}
}

func TestEditor_HistoryNavigation_BackspaceWithMultibyte(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetHistory([]string{"hi👨‍👩‍👧x"})

	ed.navigateHistory(-1)
	if got, want := ed.Text(), "hi👨‍👩‍👧x"; got != want {
		t.Fatalf("history recall: got %q, want %q", got, want)
	}
	ed.HandleInput(KeyBackspace)
	if got, want := ed.Text(), "hi👨‍👩‍👧"; got != want {
		t.Errorf("after backspace: got %q, want %q", got, want)
	}
}

// TestEditor_UpArrow_AtStartOfFirstLine_RecallsHistory verifies that pressing
// Up when the cursor is at the start of the first line recalls history,
// instead of being a no-op.
func TestEditor_UpArrow_AtStartOfFirstLine_RecallsHistory(t *testing.T) {
	ed := NewEditor()
	ed.SetHistory([]string{"a", "b"})
	ed.SetFocused(true)

	ed.SetText("summarize ci")
	ed.pos = 0
	ed.HandleInput(KeyUp)

	if got, want := ed.Text(), "b"; got != want {
		t.Errorf("Text() = %q, want %q", got, want)
	}
	if got, want := ed.histIdx, 1; got != want {
		t.Errorf("histIdx = %d, want %d", got, want)
	}
}


// TestEditor_UpArrow_AfterSubmit_RecallsLast verifies that pressing Up after
// submitting text recalls the just-submitted entry.
func TestEditor_UpArrow_AfterSubmit_RecallsLast(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetOnSubmit(func(string) {})

	ed.SetText("summarize ci")
	ed.HandleInput(KeyEnter)

	if ed.Text() != "" {
		t.Fatalf("expected empty buffer after submit, got %q", ed.Text())
	}

	ed.HandleInput(KeyUp)
	if got, want := ed.Text(), "summarize ci"; got != want {
		t.Errorf("Text() after Up = %q, want %q", got, want)
	}
}

