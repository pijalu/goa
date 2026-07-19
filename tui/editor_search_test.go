// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strconv"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// TestHistorySearcher_EmptyQueryShowsRecent covers bugs.md "inputline search":
// pressing the search hotkey must immediately show the last history entries
// even before the user types anything. Complete("") must not be empty.
func TestHistorySearcher_EmptyQueryShowsRecent(t *testing.T) {
	hist := make([]string, 0, 15)
	for i := 1; i <= 15; i++ {
		hist = append(hist, "cmd"+strconv.Itoa(i))
	}
	s := NewHistorySearcher(hist)
	got := s.Complete("")
	if len(got) == 0 {
		t.Fatal("Complete(\"\") returned no results; want last 10 history entries")
	}
	if len(got) != 10 {
		t.Errorf("Complete(\"\") returned %d results, want 10", len(got))
	}
	// Newest-first: the most recent entry (cmd15) must be first.
	if got[0].Value != "cmd15" {
		t.Errorf("Complete(\"\")[0] = %q, want newest entry %q", got[0].Value, "cmd15")
	}
}

// TestHistorySearcher_EmptyQueryDedupes ensures the recent-entries list does
// not repeat duplicated commands.
func TestHistorySearcher_EmptyQueryDedupes(t *testing.T) {
	s := NewHistorySearcher([]string{"build", "test", "build", "test", "run"})
	got := s.Complete("")
	seen := map[string]bool{}
	for _, c := range got {
		if seen[c.Value] {
			t.Errorf("duplicate entry %q in recent list", c.Value)
		}
		seen[c.Value] = true
	}
	if len(got) != 3 {
		t.Errorf("Complete(\"\") = %d results, want 3 unique", len(got))
	}
}

// TestEditor_CtrlS_EntersSearchMode covers bugs.md: ctrl+s outside search mode
// printed "ctrl+s" literally into the input line. It must instead enter
// history-search mode and show the popup.
func TestEditor_CtrlS_EntersSearchMode(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetHistory([]string{"alpha", "beta", "gamma"})

	ed.HandleInput("ctrl+s")

	if !ed.searchMode {
		t.Error("ctrl+s should enter search mode")
	}
	if strings.Contains(ed.Text(), "ctrl+s") {
		t.Errorf("ctrl+s leaked into buffer as literal text: %q", ed.Text())
	}
	if !ed.compState.Active() {
		t.Error("search popup should be active after ctrl+s (empty query shows recent)")
	}
}

// TestEditor_CtrlR_ShowsPopupOnEmptyQuery covers bugs.md: ctrl+r showed no
// box until the user typed. With an empty query the popup must list history.
func TestEditor_CtrlR_ShowsPopupOnEmptyQuery(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetHistory([]string{"alpha", "beta", "gamma"})

	ed.HandleInput("ctrl+r")

	if !ed.searchMode {
		t.Fatal("ctrl+r should enter search mode")
	}
	if !ed.compState.Active() {
		t.Error("search popup should be visible immediately on ctrl+r")
	}
	if len(ed.compState.Items) == 0 {
		t.Error("search popup should list history entries on empty query")
	}
}

// TestEditor_SearchPopupSurvivesEmptyingQuery covers the requirement that the
// search results stay navigable even after the user empties the query.
func TestEditor_SearchPopupSurvivesEmptyingQuery(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetHistory([]string{"alpha", "beta", "gamma"})

	ed.HandleInput("ctrl+r")
	ed.HandleInput("a") // filter
	if !ed.compState.Active() {
		t.Fatal("popup should be active after typing a filter")
	}
	// Empty the query back out.
	ed.HandleInput("ctrl+u") // kill to start = clear line
	if !ed.searchMode {
		t.Fatal("should still be in search mode after clearing query")
	}
	if !ed.compState.Active() {
		t.Error("popup must stay navigable after the query is emptied")
	}
}

// TestEditor_SearchArrowNavigation ensures Up/Down navigate the search popup.
func TestEditor_SearchArrowNavigation(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetHistory([]string{"alpha", "beta", "gamma"})

	ed.HandleInput("ctrl+r")
	if !ed.compState.Active() {
		t.Fatal("popup should be active")
	}
	start := ed.compState.Idx
	ed.HandleInput("down")
	if ed.compState.Idx == start {
		t.Error("down arrow should move the search selection")
	}
}

// TestHistorySearcher_SetsDisplayAndCategory covers the live report that the
// search popup rendered blank rows under a "Most Used" header: the popup
// renders Completion.Display (HistorySearcher only set Value) and groups by
// Completion.Category (zero value = CatMostUsed).
func TestHistorySearcher_SetsDisplayAndCategory(t *testing.T) {
	s := NewHistorySearcher([]string{"git status", "go build"})
	for _, c := range s.Complete("") {
		if c.Display == "" {
			t.Errorf("Completion.Display empty for %q — popup renders blank", c.Value)
		}
		if c.Display != c.Value {
			t.Errorf("Completion.Display = %q, want %q", c.Display, c.Value)
		}
		if c.Category == CatMostUsed {
			t.Errorf("history item %q must not be CatMostUsed (shows 'Most Used' header)", c.Value)
		}
	}
}

// TestEditor_SearchModeSetsTitle covers the request that the input shows a
// "history search" title while searching, cleared on exit.
func TestEditor_SearchModeSetsTitle(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetHistory([]string{"alpha", "beta"})

	ed.HandleInput("ctrl+r")
	if !strings.Contains(strings.ToLower(ed.Title()), "history search") {
		t.Errorf("search mode should title the input 'history search', got %q", ed.Title())
	}
	ed.HandleInput(KeyEscape)
	if ed.Title() != "" {
		t.Errorf("exiting search should clear the title, got %q", ed.Title())
	}
}

// TestEditor_SearchPopupRendersEntryText drives the popup renderer itself:
// the visible lines must contain the history entry text (not blank rows).
func TestEditor_SearchPopupRendersEntryText(t *testing.T) {
	ed := NewEditor()
	ed.SetFocused(true)
	ed.SetHistory([]string{"git status", "go build ./...", "go test ./tui/"})

	ed.HandleInput("ctrl+r")
	lines := ed.PopupLines(60)
	var sb strings.Builder
	for _, l := range lines {
		sb.WriteString(ansi.Strip(l) + "\n")
	}
	joined := sb.String()
	if !strings.Contains(joined, "go test ./tui/") {
		t.Errorf("popup should render the newest history entry text, got:\n%s", joined)
	}
	if strings.Contains(joined, "Most Used") {
		t.Errorf("history search popup must not show a 'Most Used' header, got:\n%s", joined)
	}
}
