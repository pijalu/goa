// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func TestNewSelector_SortsItems(t *testing.T) {
	items := []SelectorItem{
		{Value: "z", Label: "Zebra"},
		{Value: "a", Label: "alpha"},
		{Value: "m", Label: "Mango"},
	}
	result := make(chan string, 1)
	s := NewSelector("Test", items, "m", result)

	if len(s.items) != 3 {
		t.Fatalf("expected 3 items, got %d", len(s.items))
	}
	expected := []string{"alpha", "Mango", "Zebra"}
	for i, e := range expected {
		if s.items[i].Label != e {
			t.Errorf("item[%d].Label = %q, want %q", i, s.items[i].Label, e)
		}
	}
}

func TestNewSelector_SortsCaseInsensitive(t *testing.T) {
	items := []SelectorItem{
		{Value: "z", Label: "ZEBRA"},
		{Value: "a", Label: "apple"},
		{Value: "m", Label: "Mango"},
	}
	result := make(chan string, 1)
	s := NewSelector("Test", items, "", result)

	// Case-insensitive: apple < Mango < ZEBRA
	if s.items[0].Label != "apple" {
		t.Errorf("expected apple first, got %s", s.items[0].Label)
	}
	if s.items[1].Label != "Mango" {
		t.Errorf("expected Mango second, got %s", s.items[1].Label)
	}
	if s.items[2].Label != "ZEBRA" {
		t.Errorf("expected ZEBRA last, got %s", s.items[2].Label)
	}
}

// TestNewSelector_PreserveOrder pins the opt-out from alphabetical sorting
// (Session command: list ordering): when every item sets
// PreserveOrder, the caller's order is kept and the cursor starts on item 0
// — the newest-first session list stays newest-on-top and preselected.
func TestNewSelector_PreserveOrder(t *testing.T) {
	items := []SelectorItem{
		{Value: "new", Label: "15:04  newest", PreserveOrder: true},
		{Value: "mid", Label: "14:00  middle", PreserveOrder: true},
		{Value: "old", Label: "09:12  oldest", PreserveOrder: true},
	}
	result := make(chan string, 1)
	s := NewSelector("Test", items, "", result)

	want := []string{"new", "mid", "old"}
	for i, w := range want {
		if s.items[i].Value != w {
			t.Fatalf("order not preserved: items[%d].Value=%q, want %q (all=%v)", i, s.items[i].Value, w, s.items)
		}
	}
	if s.selected != 0 {
		t.Errorf("cursor must default to item 0 when currentValue matches nothing, got %d", s.selected)
	}
}

// TestNewSelector_PreserveOrderMixedFallsBackToSort: a single item without
// PreserveOrder opts the whole list back into alphabetical sorting, so no
// caller can accidentally freeze a half-ordered list.
func TestNewSelector_PreserveOrderMixedFallsBackToSort(t *testing.T) {
	items := []SelectorItem{
		{Value: "z", Label: "zebra", PreserveOrder: true},
		{Value: "a", Label: "alpha"}, // not set → whole list sorts
	}
	result := make(chan string, 1)
	s := NewSelector("Test", items, "", result)
	if s.items[0].Label != "alpha" {
		t.Fatalf("mixed list must sort alphabetically, got %v", s.items)
	}
}

// TestSelector_SetItemsPreserveOrder: the async-loading path (SetItems)
// follows the same ordering rule as the constructor.
func TestSelector_SetItemsPreserveOrder(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{{Value: "x", Label: "x"}}, "", result)
	s.SetItems([]SelectorItem{
		{Value: "b", Label: "02 second", PreserveOrder: true},
		{Value: "a", Label: "01 first", PreserveOrder: true},
	})
	if s.items[0].Value != "b" || s.items[1].Value != "a" {
		t.Fatalf("SetItems must honor PreserveOrder, got %v", s.items)
	}
}

func TestNewSelector_StartsOnCurrentValue(t *testing.T) {
	items := []SelectorItem{
		{Value: "z", Label: "Zebra"},
		{Value: "a", Label: "alpha"},
		{Value: "m", Label: "Mango"},
	}
	result := make(chan string, 1)
	s := NewSelector("Test", items, "m", result)

	// Items are sorted alphabetically; Mango (current) is at index 1.
	if s.selected != 1 {
		t.Errorf("selected = %d, want 1 (Mango)", s.selected)
	}
	if s.filtered[s.selected].Value != "m" {
		t.Errorf("filtered[selected].Value = %q, want m", s.filtered[s.selected].Value)
	}
}

func TestSelector_RenderTitle(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Select mode:", []SelectorItem{
		{Value: "coder", Label: "coder"},
	}, "", result)

	lines := s.Render(50)
	if len(lines) < 1 {
		t.Fatal("expected at least 1 line")
	}
	if !strings.Contains(lines[0], "Select mode:") {
		t.Errorf("title line should contain 'Select mode:', got %q", lines[0])
	}
	if !strings.Contains(lines[0], ansi.Bold) {
		t.Errorf("title should be bold, got %q", lines[0])
	}
}

func TestSelector_RenderCurrentValueMarked(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "cur", Label: "current"},
		{Value: "oth", Label: "other"},
	}, "cur", result)

	lines := s.Render(50)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "✓") && strings.Contains(line, "current") {
			found = true
			break
		}
	}
	if !found {
		t.Error("current value should show ✓ marker, none found in render output")
	}
}

func TestSelector_RenderSelectedHighlight(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
	}, "", result)

	lines := s.Render(50)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "›") {
			found = true
			// Selected item should use success color
			if !strings.Contains(line, ansi.Fg(TheTheme.ColorHex("tool_success"))) {
				t.Errorf("selected item should use tool_success color for ›, got %q", line)
			}
			break
		}
	}
	if !found {
		t.Error("selected item should show › marker, none found in render output")
	}
}

func TestSelector_RenderCancelHint(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	lines := s.Render(50)
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "esc") {
		t.Errorf("last line should contain esc hint, got %q", lastLine)
	}
	// Hint should use faint ANSI
	if !strings.Contains(lastLine, ansi.Faint) {
		t.Errorf("cancel hint should be dim/faint, got %q", lastLine)
	}
}

func TestSelector_RenderNoMatches(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
	}, "", result)

	// Type a filter that matches nothing
	s.HandleInput("x")
	lines := s.Render(50)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "no matches") {
			found = true
			break
		}
	}
	if !found {
		t.Error("should show 'no matches' when filter matches nothing")
	}
}

func TestSelector_FilterMatching(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
		{Value: "g", Label: "gamma"},
	}, "", result)

	// Send filter characters one at a time
	s.HandleInput("a")
	s.HandleInput("l")

	if len(s.filtered) != 1 {
		t.Errorf("expected 1 filtered item for 'al', got %d", len(s.filtered))
	}
	if len(s.filtered) > 0 && s.filtered[0].Value != "a" {
		t.Errorf("expected filtered value 'a', got %q", s.filtered[0].Value)
	}
}

func TestSelector_FilterMatchesDescription(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha", Description: "first letter"},
		{Value: "b", Label: "beta", Description: "second letter"},
	}, "", result)

	s.HandleInput("s")
	s.HandleInput("e")
	s.HandleInput("c")

	if len(s.filtered) != 1 {
		t.Errorf("expected 1 filtered item matching description, got %d", len(s.filtered))
	}
	if len(s.filtered) > 0 && s.filtered[0].Value != "b" {
		t.Errorf("expected filtered value 'b', got %q", s.filtered[0].Value)
	}
}

// TestSelector_FilterSearchLabelExcludesDescription verifies that an item
// with SearchLabel set is matched ONLY against that label: terms present
// solely in the Description (e.g. the model picker's "model=" prefix) must
// not match, while terms in the SearchLabel (model name / provider name)
// still do.
func TestSelector_FilterSearchLabelExcludesDescription(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Select model:", []SelectorItem{
		{Value: "a", Label: "alpha", Description: "model=alpha provider=p1", SearchLabel: "alpha p1 alpha"},
		{Value: "b", Label: "beta", Description: "model=beta provider=p2", SearchLabel: "beta p2 beta"},
	}, "", result)

	// "model" appears in both Descriptions but in neither SearchLabel:
	// without the fix both rows match; with SearchLabel nothing matches.
	for _, r := range "model" {
		s.HandleInput(string(r))
	}
	if len(s.filtered) != 0 {
		t.Fatalf("SearchLabel must exclude Description: %d rows matched 'model', want 0", len(s.filtered))
	}

	// Clearing the filter and typing a provider name still matches its row.
	for len(s.searchText) > 0 {
		s.HandleInput("backspace")
	}
	s.HandleInput("p")
	s.HandleInput("2")
	if len(s.filtered) != 1 || s.filtered[0].Value != "b" {
		t.Fatalf("SearchLabel provider match: got %v, want only b", s.filtered)
	}
}

// TestSelector_MinusOnSentinelDoesNotSearch is the regression for "-' does
// not work on provider, it types '-' in the search": pressing '-' while a
// sentinel item (__add__/__remove__) is highlighted must not pollute the
// search filter.
func TestSelector_MinusOnSentinelDoesNotSearch(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "__add__", Label: "— add provider —"},
		{Value: "zai", Label: "zai"},
	}, "__add__", result) // cursor starts on the sentinel

	s.HandleInput("-")

	if s.searchText != "" {
		t.Fatalf("searchText = %q after '-' on sentinel, want empty", s.searchText)
	}
	select {
	case v := <-result:
		t.Fatalf("unexpected emit %q: '-' on a sentinel must be consumed, not emitted", v)
	default:
	}
}

// TestSelector_MinusOnDeletableEmitsDelete verifies '-' on a normal item
// emits the __delete__ sentinel (unchanged behavior).
func TestSelector_MinusOnDeletableEmitsDelete(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "__add__", Label: "— add provider —"},
		{Value: "zai", Label: "zai"},
	}, "zai", result) // cursor on the deletable item

	s.HandleInput("-")

	select {
	case v := <-result:
		if v != "__delete__zai" {
			t.Fatalf("emit = %q, want __delete__zai", v)
		}
	default:
		t.Fatal("expected __delete__ emit for '-' on a deletable item")
	}
	if s.searchText != "" {
		t.Fatalf("searchText = %q, want empty", s.searchText)
	}
}

// TestSelector_EditHotkeyEmitsEditSentinel verifies 'e' on an Editable item
// (empty filter) emits "__edit__"+value — the /goal:manage edit affordance —
// without touching the search filter.
func TestSelector_EditHotkeyEmitsEditSentinel(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "qg1", Label: "first goal", Editable: true},
		{Value: "qg2", Label: "second goal", Editable: true},
	}, "qg2", result) // cursor on the second goal

	s.HandleInput("e")

	select {
	case v := <-result:
		if v != "__edit__qg2" {
			t.Fatalf("emit = %q, want __edit__qg2", v)
		}
	default:
		t.Fatal("expected __edit__ emit for 'e' on an editable item")
	}
	if s.searchText != "" {
		t.Fatalf("searchText = %q, want empty", s.searchText)
	}
}

// TestSelector_EditHotkeyFallsBackToFilter verifies 'e' keeps its default
// behavior everywhere else: on non-editable rows (no opt-in) it starts a
// filter, and with a non-empty filter it appends even when the highlighted
// item is editable.
func TestSelector_EditHotkeyFallsBackToFilter(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "echo", Label: "echo"},
		{Value: "emacs", Label: "emacs"},
	}, "", result)

	s.HandleInput("e")

	if s.searchText != "e" {
		t.Fatalf("searchText = %q, want e (filter fallback on non-editable item)", s.searchText)
	}
	select {
	case v := <-result:
		t.Fatalf("unexpected emit %q: 'e' on a non-editable item must filter, not emit", v)
	default:
	}

	// Non-empty filter: 'e' appends even when an editable item is highlighted.
	s2 := NewSelector("Test", []SelectorItem{
		{Value: "keep", Label: "keep", Editable: true},
		{Value: "kilo", Label: "kilo", Editable: true},
	}, "", make(chan string, 1))
	s2.HandleInput("k")
	s2.HandleInput("e")
	if s2.searchText != "ke" {
		t.Fatalf("searchText = %q, want ke (mid-word 'e' must filter)", s2.searchText)
	}
	if len(s2.filtered) != 1 || s2.filtered[0].Value != "keep" {
		t.Fatalf("filtered = %+v, want only keep", s2.filtered)
	}
}

// TestSelector_BackspaceEmptyFilterConsumedNotDelete is the regression for
// two reported bugs:
//   - /config: type a filter char then Backspace (filter empties), then
//     another Backspace — previously this emitted __delete__ on the
//     highlighted row, which closed the menu. It must instead be a no-op.
//   - /model: Backspace must never propose to delete; deletion is only the
//     '-' hotkey or the Delete key.
//
// Backspace is exclusively the filter-editing key: with a non-empty filter it
// removes a char; with an empty filter it is consumed without emitting.
func TestSelector_BackspaceEmptyFilterConsumedNotDelete(t *testing.T) {
	// Default keymap (the /model, /provider pickers).
	t.Run("default keymap", func(t *testing.T) {
		result := make(chan string, 1)
		s := NewSelector("Test", []SelectorItem{
			{Value: "__add__", Label: "— add —", PreserveOrder: true},
			{Value: "zai", Label: "zai", PreserveOrder: true},
		}, "zai", result) // cursor on the deletable item
		expectNoEmit(t, s, result, KeyBackspace)
	})

	// Reorder keymap (/goal:manage).
	t.Run("reorder keymap", func(t *testing.T) {
		result := make(chan string, 1)
		s := NewSelector("Test", []SelectorItem{
			{Value: "qg1", Label: "a goal"},
		}, "qg1", result)
		s.SetKeymap(SelectorKeymap{ReorderMode: true})
		expectNoEmit(t, s, result, KeyBackspace)
	})
}

// TestSelector_BackspaceClearsFilterThenStaysOpen pins the full reported
// interaction: type a filter char, Backspace removes it (filter empties, list
// stays), a further Backspace is a no-op — the selector never emits and never
// closes. This is the exact /config "typing a letter then backspace closes
// the menu" scenario.
func TestSelector_BackspaceClearsFilterThenStaysOpen(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Settings:", []SelectorItem{
		{Value: "theme", Label: "Theme"},
		{Value: "model", Label: "Active model"},
	}, "", result)

	// Type 'o' — only "Active model" contains it, so the filter narrows to 1.
	s.HandleInput("o")
	if s.searchText != "o" || len(s.filtered) != 1 {
		t.Fatalf("after 'o': searchText=%q filtered=%d, want o / 1", s.searchText, len(s.filtered))
	}

	// Backspace clears the filter back to all items; nothing is emitted.
	expectNoEmit(t, s, result, KeyBackspace)
	if len(s.filtered) != 2 {
		t.Fatalf("after clearing filter: filtered=%d, want 2", len(s.filtered))
	}

	// A second Backspace (now-empty filter) must also be a no-op — no delete,
	// no close.
	expectNoEmit(t, s, result, KeyBackspace)
	if len(s.filtered) != 2 {
		t.Fatalf("after empty-filter backspace: filtered=%d, want 2", len(s.filtered))
	}
}

// TestSelector_DeleteKeyStillDeletesWithEmptyFilter ensures removing the
// Backspace delete trigger did not disable the dedicated Delete key: with an
// empty filter, Delete still emits __delete__+value on a non-sentinel row.
func TestSelector_DeleteKeyStillDeletesWithEmptyFilter(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "__add__", Label: "— add —", PreserveOrder: true},
		{Value: "zai", Label: "zai", PreserveOrder: true},
	}, "zai", result)
	if got := emitResult(t, s, result, KeyDelete); got != "__delete__zai" {
		t.Errorf("emit = %q, want __delete__zai", got)
	}
}

// selectorFrame renders the selector into an AgentFrame so the filmstrip can
// diff the visible list across the interaction (the agent-testable view of
// the widget, per filmstrip.go).
func selectorFrame(s *Selector) AgentFrame {
	lines := s.Render(60)
	visible := make([]string, len(lines))
	for i, l := range lines {
		visible[i] = ansi.Strip(l)
	}
	return AgentFrame{Width: 60, Height: len(visible), Visible: visible}
}

// TestSelector_BackspaceFilmstripMenuStaysOpen is the filmstrip regression
// for the two reported bugs: it captures a Snapshot of the selector at each
// step of "type a filter letter → Backspace (clears the filter) → Backspace
// (empty filter)" and asserts, as data, that the list never closes and
// Backspace never emits a __delete__ — the rendered menu stays on screen
// through the whole sequence.
func TestSelector_BackspaceFilmstripMenuStaysOpen(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Select model:", []SelectorItem{
		{Value: "gpt4", Label: "gpt-4"},
		{Value: "claude", Label: "claude"},
	}, "gpt4", result)

	film := NewFilmstrip()
	film.Capture("open", selectorFrame(s), "")

	// Step 1: type 'g' — filter narrows to the matching row.
	s.HandleInput("g")
	film.Capture("type 'g'", selectorFrame(s), "")
	if len(s.filtered) != 1 {
		t.Fatalf("after 'g': filtered=%d, want 1\n%s", len(s.filtered), film.Render())
	}

	// Step 2: Backspace clears the filter — the full list reappears, nothing
	// emitted (the /config "type a letter then backspace closes the menu" bug).
	s.HandleInput(KeyBackspace)
	restored := film.Capture("backspace clears filter", selectorFrame(s), "")
	if len(s.filtered) != 2 {
		t.Fatalf("after clearing backspace: filtered=%d, want 2 (menu must stay open)\n%s", len(s.filtered), film.Render())
	}

	// Step 3: a further Backspace on the now-empty filter must be a no-op —
	// no __delete__ emit, no close (the /model "backspace proposes delete" bug).
	before := len(film.Frames())
	s.HandleInput(KeyBackspace)
	film.Capture("backspace on empty filter", selectorFrame(s), "")
	if len(film.Frames()) != before+1 {
		t.Fatalf("expected a capture step, frames=%d", len(film.Frames()))
	}

	// No frame in the whole sequence may have delivered a result (which would
	// hide the overlay / trigger delete).
	select {
	case v := <-result:
		t.Fatalf("unexpected emit %q: backspace must never delete/close\n%s", v, film.Render())
	default:
	}

	// The visible list still shows both rows after the full interaction.
	last := film.Last()
	if last == nil {
		t.Fatal("filmstrip must have a last frame")
	}
	joined := joinLines(last.Frame.Visible)
	for _, want := range []string{"Select model:", "gpt-4", "claude"} {
		if !strings.Contains(joined, want) {
			t.Errorf("final frame missing %q — the menu closed early\n%s", want, film.Render())
		}
	}

	// The diff into "restored" must show claude reappearing (the filter
	// widened), proving backspace widened rather than closed the list.
	if !strings.Contains(joinLines(restored.Diff.AddedLines), "claude") {
		t.Errorf("after clearing the filter 'claude' should reappear in the diff, added=%v\n%s",
			restored.Diff.AddedLines, film.Render())
	}
}

// TestSelector_MinusMidWordStillSearches verifies '-' keeps working as a
// search character once the filter is non-empty (e.g. "glm-4.5").
func TestSelector_MinusMidWordStillSearches(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "glm-4.5", Label: "glm-4.5"},
		{Value: "glm-4.7", Label: "glm-4.7"},
	}, "", result)

	s.HandleInput("g")
	s.HandleInput("l")
	s.HandleInput("m")
	s.HandleInput("-")

	if s.searchText != "glm-" {
		t.Fatalf("searchText = %q, want glm-", s.searchText)
	}
	if len(s.filtered) != 2 {
		t.Fatalf("filtered = %d items for glm-, want 2", len(s.filtered))
	}
}

// TestSelector_MinusOnDeletePrefixedItemEmitsDelete is the regression for the
// "Model delete" bug: a model whose persisted ID begins with "__delete__" (a
// leaked sentinel saved as a model ID) must still be deletable via '-'. The
// previous "__" prefix guard treated it as a sentinel and swallowed the key,
// making the polluted entry impossible to remove.
func TestSelector_MinusOnDeletePrefixedItemEmitsDelete(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "__delete__deepseek-v4-flash", Label: "__delete__deepseek-v4-flash"},
		{Value: "k3", Label: "k3"},
	}, "__delete__deepseek-v4-flash", result) // cursor on the polluted item

	s.HandleInput("-")

	select {
	case v := <-result:
		// Selector prepends __delete__; model.go's TrimPrefix("__delete__")
		// recovers the real (polluted) ID for removal.
		if v != "__delete____delete__deepseek-v4-flash" {
			t.Fatalf("emit = %q, want __delete____delete__deepseek-v4-flash", v)
		}
	default:
		t.Fatal("expected __delete__ emit for '-' on a __delete__-prefixed item")
	}
	if s.searchText != "" {
		t.Fatalf("searchText = %q, want empty", s.searchText)
	}
}

// TestSelector_MinusOnCustomSentinelConsumed verifies the '__custom__' action
// row remains non-deletable: '-' must be consumed, not emitted.
func TestSelector_MinusOnCustomSentinelConsumed(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "__custom__", Label: "── custom model ──"},
		{Value: "k3", Label: "k3"},
	}, "__custom__", result) // cursor on the sentinel

	s.HandleInput("-")

	if s.searchText != "" {
		t.Fatalf("searchText = %q after '-' on __custom__, want empty", s.searchText)
	}
	select {
	case v := <-result:
		t.Fatalf("unexpected emit %q: '-' on __custom__ must be consumed", v)
	default:
	}
}

func TestSelector_FilterBackspace(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "z", Label: "zebra"},
		{Value: "x", Label: "xylophone"},
	}, "", result)

	s.HandleInput("z")
	if len(s.filtered) != 1 {
		t.Errorf("expected 1 filtered item after 'z', got %d", len(s.filtered))
	}

	// Backspace
	s.HandleInput("backspace")
	if len(s.filtered) != 2 {
		t.Errorf("expected all 2 items after backspace, got %d", len(s.filtered))
	}
}

func TestSelector_NavigateDown(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
		{Value: "c", Label: "gamma"},
	}, "", result)

	if s.selected != 0 {
		t.Errorf("expected initial selected=0, got %d", s.selected)
	}

	s.HandleInput(KeyDown)
	if s.selected != 1 {
		t.Errorf("expected selected=1 after first down, got %d", s.selected)
	}

	s.HandleInput(KeyDown)
	if s.selected != 2 {
		t.Errorf("expected selected=2 after second down, got %d", s.selected)
	}
}

func TestSelector_NavigateUp(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
	}, "", result)

	s.selected = 1
	s.HandleInput(KeyUp)
	if s.selected != 0 {
		t.Errorf("expected selected=0 after up, got %d", s.selected)
	}

	// Wrap around
	s.HandleInput(KeyUp)
	if s.selected != 1 {
		t.Errorf("expected selected=1 wrapping around, got %d", s.selected)
	}
}

func TestSelector_EnterSelects(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "alpha", Label: "alpha"},
		{Value: "beta", Label: "beta"},
	}, "", result)

	// Tab to accept first
	s.HandleInput(KeyTab)
	select {
	case v := <-result:
		if v != "alpha" {
			t.Errorf("expected 'alpha', got %q", v)
		}
	default:
		t.Error("expected result to be delivered on Tab")
	}
}

func TestSelector_EscapeCancels(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	s.HandleInput(KeyEscape)
	select {
	case v := <-result:
		if v != "" {
			t.Errorf("expected empty string on cancel, got %q", v)
		}
	default:
		t.Error("expected result on escape")
	}
}

func TestSelector_CtrlCCancels(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	s.HandleInput(KeyCtrlC)
	select {
	case v := <-result:
		if v != "" {
			t.Errorf("expected empty string on cancel, got %q", v)
		}
	default:
		t.Error("expected result on Ctrl+C")
	}
}

func TestSelector_ApplyFilterResetsSelected(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
		{Value: "g", Label: "gamma"},
	}, "", result)

	s.selected = 2
	s.HandleInput("a")
	// After filtering to just "alpha", selected should be 0
	if s.selected >= len(s.filtered) {
		t.Errorf("selected=%d should be within filtered count=%d", s.selected, len(s.filtered))
	}
}

func TestSelector_RenderWidthClamped(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	// Very large width should be clamped to max 60
	lines := s.Render(200)
	for _, line := range lines {
		vw := visibleWidth(line)
		// The hint line can exceed 60 due to wide Unicode arrows (↑↓).
		// Allow up to 75 for hint text; all other lines must be ≤ 62.
		if vw > 75 {
			t.Errorf("line visual width too wide: vw=%d, line=%q", vw, line)
		}
	}
}

func TestSelector_RenderEmptyList(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{}, "", result)

	lines := s.Render(50)
	if len(lines) == 0 {
		t.Fatal("expected at least some lines for empty list")
	}
}

func TestSelector_SetItems(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "b", Label: "banana"},
	}, "", result)

	s.SetItems([]SelectorItem{
		{Value: "a", Label: "apple"},
		{Value: "b", Label: "banana"},
	})

	if len(s.items) != 2 {
		t.Errorf("expected 2 items after SetItems, got %d", len(s.items))
	}
	if s.items[0].Label != "apple" {
		t.Errorf("expected apple sorted first, got %s", s.items[0].Label)
	}
}

func TestSelector_StartWithSelect(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
		{Value: "b", Label: "beta"},
	}, "", result)

	// Enter on first item (selected=0)
	s.HandleInput(KeyEnter)
	select {
	case v := <-result:
		if v != "a" {
			t.Errorf("expected 'a', got %q", v)
		}
	default:
		t.Error("expected result on Enter")
	}
}

func TestSelector_RenderSeparators(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	lines := s.Render(50)
	// Should have at least 3 separators (title separator, search separator, hint separator)
	sepCount := 0
	for _, line := range lines {
		if strings.Contains(line, "─") {
			sepCount++
		}
	}
	if sepCount < 3 {
		t.Errorf("expected at least 3 separator lines, got %d", sepCount)
	}
}

func TestSelector_RenderDescription(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha", Description: "first letter"},
	}, "", result)

	lines := s.Render(50)
	found := false
	for _, line := range lines {
		if strings.Contains(line, "first letter") {
			found = true
			break
		}
	}
	if !found {
		t.Error("description should be rendered in selector output")
	}
}

func TestSelector_NeedsFilter(t *testing.T) {
	result := make(chan string, 1)
	// With <= 5 items, the hint should still show filter text
	s1 := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "a"},
		{Value: "b", Label: "b"},
	}, "", result)
	lines := s1.Render(50)
	lastLine := lines[len(lines)-1]
	if !strings.Contains(lastLine, "filter") {
		t.Error("hint should contain filter text even for small lists")
	}

	// With > 5 items, the hint should also show filter text
	s2 := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "a"},
		{Value: "b", Label: "b"},
		{Value: "c", Label: "c"},
		{Value: "d", Label: "d"},
		{Value: "e", Label: "e"},
		{Value: "f", Label: "f"},
	}, "", result)
	lines2 := s2.Render(50)
	lastLine2 := lines2[len(lines2)-1]
	if !strings.Contains(lastLine2, "filter") {
		t.Error("hint should contain filter text even for large lists")
	}
}

func TestSelector_EmitDoneCalled(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	doneCalled := false
	s.SetDone(func() {
		doneCalled = true
	})

	s.HandleInput(KeyEnter)
	if !doneCalled {
		t.Error("done callback should be called on select")
	}
}

func TestSelector_EmitDoneCalledOnCancel(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	doneCalled := false
	s.SetDone(func() {
		doneCalled = true
	})

	s.HandleInput(KeyEscape)
	if !doneCalled {
		t.Error("done callback should be called on cancel")
	}
}

func TestSelector_RenderZeroWidth(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	lines := s.Render(0)
	if len(lines) < 3 {
		t.Errorf("expected at least 3 lines with min width, got %d", len(lines))
	}
}

func TestSelector_RenderSmallWidth(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "a", Label: "alpha"},
	}, "", result)

	// Width 10 should be clamped to min 30
	lines := s.Render(10)
	// Should still work without panic
	if len(lines) < 3 {
		t.Errorf("expected at least 3 lines, got %d", len(lines))
	}
}

// emitResult sends one key and expects exactly one emit on the result channel.
func emitResult(t *testing.T, s *Selector, result chan string, key string) string {
	t.Helper()
	s.HandleInput(key)
	select {
	case v := <-result:
		return v
	default:
		t.Fatalf("expected an emit for key %q", key)
		return ""
	}
}

// expectNoEmit sends one key and expects it to be consumed (no emit).
func expectNoEmit(t *testing.T, s *Selector, result chan string, key string) {
	t.Helper()
	s.HandleInput(key)
	select {
	case v := <-result:
		t.Fatalf("unexpected emit %q for key %q: the key must be consumed", v, key)
	default:
	}
	if s.searchText != "" {
		t.Fatalf("searchText = %q after key %q, want empty (consumed keys never filter)", s.searchText, key)
	}
}

// TestSelector_ReorderModeMoveEmits pins the reorder keymap (/goal:manage):
// '+'/'-' on a non-sentinel row emit __moveup__/__movedown__ + value and
// never touch the search filter. The active-goal row ("__active__") is a
// regular row for the selector — the manager rejects its reordering.
func TestSelector_ReorderModeMoveEmits(t *testing.T) {
	items := []SelectorItem{
		{Value: "__add_first__", Label: "-- add at start --", PreserveOrder: true},
		{Value: "__active__", Label: "[active] running", PreserveOrder: true},
		{Value: "qg1", Label: "first goal", PreserveOrder: true},
		{Value: "qg2", Label: "second goal", PreserveOrder: true},
		{Value: "__add_last__", Label: "-- add at end --", PreserveOrder: true},
		{Value: "__done__", Label: "Done", PreserveOrder: true},
	}
	cases := []struct {
		name   string
		cursor string
		key    string
		want   string
	}{
		{"plus on goal", "qg2", "+", "__moveup__qg2"},
		{"minus on goal", "qg2", "-", "__movedown__qg2"},
		{"plus on active row", "__active__", "+", "__moveup____active__"},
		{"minus on active row", "__active__", "-", "__movedown____active__"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := make(chan string, 1)
			s := NewSelector("Test", items, tc.cursor, result)
			s.SetKeymap(SelectorKeymap{ReorderMode: true})
			if got := emitResult(t, s, result, tc.key); got != tc.want {
				t.Errorf("emit = %q, want %q", got, tc.want)
			}
			if s.searchText != "" {
				t.Errorf("searchText = %q, want empty", s.searchText)
			}
		})
	}
}

// TestSelector_ReorderModeSentinelsConsumed: in reorder mode the '+/-' keys
// are consumed on the manager's sentinel rows (add rows, Done) — no emit,
// no search-filter pollution.
func TestSelector_ReorderModeSentinelsConsumed(t *testing.T) {
	sentinels := []string{"__add_first__", "__add_last__", "__done__"}
	keys := []string{"+", "-"}
	for _, sentinel := range sentinels {
		for _, key := range keys {
			t.Run(sentinel+"/"+key, func(t *testing.T) {
				result := make(chan string, 1)
				s := NewSelector("Test", []SelectorItem{
					{Value: sentinel, Label: sentinel, PreserveOrder: true},
					{Value: "qg1", Label: "a goal", PreserveOrder: true},
				}, sentinel, result)
				s.SetKeymap(SelectorKeymap{ReorderMode: true})
				expectNoEmit(t, s, result, key)
			})
		}
	}
}

// TestSelector_ReorderModeDeleteStillEmits: the Delete key keeps emitting
// __delete__+value in reorder mode — the manager turns that into a confirmed
// deletion. Backspace no longer deletes (it only edits the filter); that is
// pinned separately by TestSelector_BackspaceEmptyFilterConsumedNotDelete.
func TestSelector_ReorderModeDeleteStillEmits(t *testing.T) {
	result := make(chan string, 1)
	s := NewSelector("Test", []SelectorItem{
		{Value: "qg1", Label: "a goal"},
	}, "qg1", result)
	s.SetKeymap(SelectorKeymap{ReorderMode: true})
	if got := emitResult(t, s, result, KeyDelete); got != "__delete__qg1" {
		t.Errorf("emit = %q, want __delete__qg1", got)
	}
}

// TestSelector_GoalManagerSentinelsNotDeletable: the manager's new action
// rows are registered as sentinels, so even with the DEFAULT keymap they
// cannot be deleted via '-' or Delete — previously Delete on the Done row
// emitted "__delete____done__" and surfaced a bogus "not found" error.
func TestSelector_GoalManagerSentinelsNotDeletable(t *testing.T) {
	sentinels := []string{"__add_first__", "__add_last__", "__done__"}
	keys := []string{"-", KeyDelete}
	for _, sentinel := range sentinels {
		for _, key := range keys {
			t.Run(sentinel+"/"+key, func(t *testing.T) {
				result := make(chan string, 1)
				s := NewSelector("Test", []SelectorItem{
					{Value: sentinel, Label: sentinel, PreserveOrder: true},
					{Value: "qg1", Label: "a goal", PreserveOrder: true},
				}, sentinel, result)
				expectNoEmit(t, s, result, key)
			})
		}
	}
}

// TestSelector_DefaultKeymapUnchanged is the regression guard for the
// /provider and /model pickers after the per-instance keymap was added
// (goal manager): with the zero keymap, '+' emits __add__, '-'
// emits __delete__+value, and the Delete key emits __delete__+value.
// Backspace is no longer a delete trigger (it only edits the search filter);
// see TestSelector_BackspaceEmptyFilterConsumedNotDelete.
func TestSelector_DefaultKeymapUnchanged(t *testing.T) {
	cases := []struct {
		name   string
		cursor string
		key    string
		want   string
	}{
		{"plus adds", "zai", "+", "__add__"},
		{"minus deletes", "zai", "-", "__delete__zai"},
		{"delete key deletes", "zai", KeyDelete, "__delete__zai"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			result := make(chan string, 1)
			s := NewSelector("Test", []SelectorItem{
				{Value: "__add__", Label: "— add provider —", PreserveOrder: true},
				{Value: "zai", Label: "zai", PreserveOrder: true},
			}, tc.cursor, result)
			if got := emitResult(t, s, result, tc.key); got != tc.want {
				t.Errorf("emit = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestSelector_ReorderModeHint: the footer hint reflects the active keymap —
// reorder hints in reorder mode, add/delete hints by default.
func TestSelector_ReorderModeHint(t *testing.T) {
	items := []SelectorItem{{Value: "qg1", Label: "a goal"}}

	reorder := NewSelector("Test", items, "", make(chan string, 1))
	reorder.SetKeymap(SelectorKeymap{ReorderMode: true})
	lines := reorder.Render(60)
	last := lines[len(lines)-1]
	if !strings.Contains(last, "+ up / - down / del delete") {
		t.Errorf("reorder-mode hint = %q, want '+ up / - down / del delete'", last)
	}
	if strings.Contains(last, "+ add") {
		t.Errorf("reorder-mode hint must not advertise '+ add': %q", last)
	}

	def := NewSelector("Test", items, "", make(chan string, 1))
	lines = def.Render(60)
	last = lines[len(lines)-1]
	if !strings.Contains(last, "+ add / - delete") {
		t.Errorf("default hint = %q, want '+ add / - delete'", last)
	}
}
