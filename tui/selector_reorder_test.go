// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

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
