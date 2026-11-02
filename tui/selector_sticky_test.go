// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
	"time"
)

// B56 contract: sticky pickers keep the overlay open on confirming a toggle
// row (Enter emits without closing), close on Escape/cancel and on rows that
// opt out via CloseOnSelect, and UpdateItems swaps items in place — highlight
// re-anchored by value, filter text preserved, filtered view refiltered.

func newB56Selector(t *testing.T, items []SelectorItem, current string, sticky bool) (*Selector, chan string, *bool) {
	t.Helper()
	result := make(chan string, 4)
	s := NewSelector("Test", items, current, result)
	if sticky {
		s.SetSticky(true)
	}
	doneCalled := false
	s.SetDone(func() { doneCalled = true })
	return s, result, &doneCalled
}

func readEmit(t *testing.T, result chan string) string {
	t.Helper()
	select {
	case v := <-result:
		return v
	case <-time.After(time.Second):
		t.Fatal("no emit delivered within 1s")
		return ""
	}
}

func assertNoEmit(t *testing.T, result chan string) {
	t.Helper()
	select {
	case v := <-result:
		t.Fatalf("unexpected emit %q", v)
	default:
	}
}

// TestSelector_StickyToggleKeepsOpen pins the core B56 behavior: Enter on a
// toggle row delivers the value but does NOT close the sticky picker.
func TestSelector_StickyToggleKeepsOpen(t *testing.T) {
	s, result, done := newB56Selector(t, []SelectorItem{
		{Value: "goal", Label: "goal", Description: "off"},
		{Value: "mcp", Label: "mcp", Description: "on"},
	}, "", true)

	s.HandleInput(KeyDown) // highlight "mcp" (sorted: goal, mcp)
	s.HandleInput(KeyEnter)

	if got := readEmit(t, result); got != "mcp" {
		t.Fatalf("emit = %q, want mcp", got)
	}
	if *done {
		t.Fatal("sticky picker closed on a toggle row: it must stay open (B56)")
	}
	if s.SelectedValue() != "mcp" {
		t.Fatalf("selection moved to %q, want mcp", s.SelectedValue())
	}

	// A second confirmation delivers again on the SAME picker.
	s.HandleInput(KeyEnter)
	if got := readEmit(t, result); got != "mcp" {
		t.Fatalf("second emit = %q, want mcp", got)
	}
	if *done {
		t.Fatal("sticky picker closed on the second toggle row")
	}
	assertNoEmit(t, result)
}

// TestSelector_StickyCloseOnSelectRowCloses pins the opt-out: navigation /
// opener rows marked CloseOnSelect close the sticky picker on Enter.
func TestSelector_StickyCloseOnSelectRowCloses(t *testing.T) {
	s, result, done := newB56Selector(t, []SelectorItem{
		{Value: "enabled", Label: "Enabled", Description: "off"},
		{Value: "advanced", Label: "Advanced…", CloseOnSelect: true},
	}, "", true)

	// Items sort case-insensitively by label: "Advanced…" lands first, so the
	// CloseOnSelect row is highlighted with no navigation at all.
	s.HandleInput(KeyEnter)

	if got := readEmit(t, result); got != "advanced" {
		t.Fatalf("emit = %q, want advanced", got)
	}
	if !*done {
		t.Fatal("CloseOnSelect row did not close the sticky picker")
	}
}

// TestSelector_StickyEscapeCloses pins the cancel path: Escape closes the
// sticky picker and delivers the "" close emit.
func TestSelector_StickyEscapeCloses(t *testing.T) {
	s, result, done := newB56Selector(t, []SelectorItem{
		{Value: "goal", Label: "goal"},
	}, "", true)

	s.HandleInput(KeyEscape)

	if got := readEmit(t, result); got != "" {
		t.Fatalf("close emit = %q, want \"\"", got)
	}
	if !*done {
		t.Fatal("Escape did not close the sticky picker")
	}
}

// TestSelector_PlainStillClosesOnEnter pins backwards compatibility: without
// SetSticky the picker closes on every confirmation, as before B56.
func TestSelector_PlainStillClosesOnEnter(t *testing.T) {
	s, result, done := newB56Selector(t, []SelectorItem{
		{Value: "goal", Label: "goal"},
	}, "", false)

	s.HandleInput(KeyEnter)

	if got := readEmit(t, result); got != "goal" {
		t.Fatalf("emit = %q, want goal", got)
	}
	if !*done {
		t.Fatal("plain picker did not close on Enter")
	}
}

// TestSelector_UpdateItemsAnchorsByValue pins the in-place refresh: the
// highlighted row stays on the same Value after the items are replaced (the
// flipped Description is visible, the cursor never jumps).
func TestSelector_UpdateItemsAnchorsByValue(t *testing.T) {
	s, _, _ := newB56Selector(t, []SelectorItem{
		{Value: "goal", Label: "goal", Description: "off"},
		{Value: "mcp", Label: "mcp", Description: "on"},
	}, "", true)

	s.HandleInput(KeyDown) // highlight "mcp"
	if s.SelectedValue() != "mcp" {
		t.Fatalf("setup: highlighted %q, want mcp", s.SelectedValue())
	}

	s.UpdateItems([]SelectorItem{
		{Value: "goal", Label: "goal", Description: "off"},
		{Value: "mcp", Label: "mcp", Description: "off"},
	})

	if s.SelectedValue() != "mcp" {
		t.Fatalf("selection jumped to %q after UpdateItems, want mcp", s.SelectedValue())
	}
	for _, it := range s.Items() {
		if it.Value == "mcp" && it.Description != "off" {
			t.Fatalf("updated description not visible: %q", it.Description)
		}
	}
}

// TestSelector_UpdateItemsKeepsFilter pins that an active search filter
// survives UpdateItems and is re-applied to the new items.
func TestSelector_UpdateItemsKeepsFilter(t *testing.T) {
	s, _, _ := newB56Selector(t, []SelectorItem{
		{Value: "goal", Label: "goal", Description: "off"},
		{Value: "mcp", Label: "mcp", Description: "on"},
	}, "", true)

	s.HandleInput("m") // filter: only mcp matches
	if got := s.SelectedValue(); got != "mcp" {
		t.Fatalf("setup: filtered highlight = %q, want mcp", got)
	}

	s.UpdateItems([]SelectorItem{
		{Value: "goal", Label: "goal", Description: "on"},
		{Value: "mcp", Label: "mcp", Description: "off"},
		{Value: "verify", Label: "verify", Description: "off"},
	})

	// Filter "m" still applied: verify must stay hidden, mcp highlighted.
	if s.SelectedValue() != "mcp" {
		t.Fatalf("highlight after filtered UpdateItems = %q, want mcp", s.SelectedValue())
	}
	for _, it := range s.filtered {
		if strings.Contains(it.Value, "verify") {
			t.Fatal("filter was dropped by UpdateItems: filtered-out row reappeared")
		}
	}
}

// TestSelector_UpdateItemsVanishedValueClamps pins the clamp: when the
// anchored row is no longer in the filtered set, the selection lands on the
// last available row instead of crashing or drifting out of range.
func TestSelector_UpdateItemsVanishedValueClamps(t *testing.T) {
	s, _, _ := newB56Selector(t, []SelectorItem{
		{Value: "a", Label: "a"},
		{Value: "b", Label: "b"},
		{Value: "c", Label: "c"},
	}, "", true)

	s.HandleInput(KeyDown)
	s.HandleInput(KeyDown)
	s.HandleInput(KeyDown) // wrap to "a"
	if s.SelectedValue() != "a" {
		t.Fatalf("setup: highlight = %q, want a", s.SelectedValue())
	}

	// New set without "a": clamp keeps the cursor inside the list.
	s.UpdateItems([]SelectorItem{
		{Value: "b", Label: "b"},
		{Value: "c", Label: "c"},
	})
	if s.selected < 0 || s.selected >= len(s.filtered) {
		t.Fatalf("selection out of range after UpdateItems: %d (len %d)", s.selected, len(s.filtered))
	}

	// Empty replacement: no panic, empty view.
	s.UpdateItems(nil)
	if len(s.filtered) != 0 {
		t.Fatalf("UpdateItems(nil) left %d filtered rows", len(s.filtered))
	}
}

// TestSelector_UpdateItemsEmptyAnchorFirst pins the no-current case: with no
// row highlighted, UpdateItems lands the cursor on the first row.
func TestSelector_UpdateItemsEmptyAnchorFirst(t *testing.T) {
	s, _, _ := newB56Selector(t, []SelectorItem{
		{Value: "goal", Label: "goal"},
	}, "", true)

	s.UpdateItems([]SelectorItem{
		{Value: "mcp", Label: "mcp"},
		{Value: "verify", Label: "verify"},
	})
	if s.SelectedValue() != "mcp" {
		t.Fatalf("highlight after UpdateItems = %q, want first row mcp", s.SelectedValue())
	}
}
