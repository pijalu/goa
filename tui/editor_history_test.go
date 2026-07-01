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
