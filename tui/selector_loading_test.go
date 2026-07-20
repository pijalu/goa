// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"testing"
)

// TestShowSelectorLoading_ShowsPlaceholderThenUpdates verifies the loading
// selector opens with a "Loading…" placeholder and replaces it when the async
// fetch pushes real items via Apply(SetItems) — the TUI loading-indicator
// feature for remotely-fetched lists (bugs.md).
func TestShowSelectorLoading_ShowsPlaceholderThenUpdates(t *testing.T) {
	term := &fakeTerminal{w: 80, h: 24}
	eng := NewTUI(term)
	eng.RunLoops()
	defer eng.Stop()

	sel, _ := eng.ShowSelectorLoading("Select model:", "")
	if sel == nil {
		t.Fatal("expected a live selector")
	}
	if len(sel.filtered) != 1 || sel.filtered[0].Label != "Loading…" {
		t.Fatalf("placeholder = %+v, want single Loading… item", sel.filtered)
	}

	// Simulate async fetch completing: push real items on the command loop.
	real := []SelectorItem{
		{Value: "glm-5.2", Label: "glm-5.2"},
		{Value: "glm-4.5", Label: "glm-4.5"},
	}
	eng.Apply(func() { sel.SetItems(real) })
	eng.ApplySync(func() {}) // flush

	if len(sel.filtered) != 2 {
		t.Fatalf("after SetItems, filtered = %d items, want 2", len(sel.filtered))
	}
	for _, it := range sel.filtered {
		if it.Label == "Loading…" {
			t.Fatal("placeholder must be replaced by real items")
		}
	}
}
