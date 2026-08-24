// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// multiRows is the M6 review-card shape: two checkbox rows with conservative
// defaults (intercept OFF, notify ON) plus accept/reject action rows.
func multiRows() []ConfirmOption {
	return []ConfirmOption{
		{ID: "intercept|tool-call:pre", Label: "[intercept] tool-call:pre — Redact AWS keys", Toggle: true},
		{ID: "notify|tool-call:post", Label: "[notify] tool-call:post — usage counters", Toggle: true, DefaultOn: true},
		{ID: "accept", Label: "Accept selected", Style: "ok"},
		{ID: "reject", Label: "Reject all", Style: "danger"},
	}
}

func newMultiCard(choose func(id string, cancelled bool)) *ConfirmCard {
	return NewMultiConfirmCard("Plugin hooks", "body", multiRows(), "", true, choose)
}

// Conservative defaults (§7 step 3): notify rows pre-checked, intercept rows
// off until explicitly enabled.
func TestMultiConfirmCard_ConservativeDefaults(t *testing.T) {
	c := newMultiCard(nil)
	if got := c.SelectedIDs(); len(got) != 1 || got[0] != "notify|tool-call:post" {
		t.Fatalf("defaults = %v, want only the notify row checked", got)
	}
}

func TestMultiConfirmCard_SpaceToggles(t *testing.T) {
	c := newMultiCard(nil)
	// Cursor starts on the intercept row (unchecked): space checks it.
	c.HandleInput(" ")
	if got := c.SelectedIDs(); len(got) != 2 {
		t.Fatalf("after toggle = %v, want both rows", got)
	}
	c.HandleInput(" ")
	if got := c.SelectedIDs(); len(got) != 1 || got[0] != "notify|tool-call:post" {
		t.Fatalf("untoggle failed: %v", got)
	}
	// Space on the action rows must be a no-op.
	c.HandleInput(KeyDown) // notify
	c.HandleInput(KeyDown) // accept
	c.HandleInput(" ")
	c.HandleInput(KeyDown) // reject
	c.HandleInput(" ")
	if got := c.SelectedIDs(); len(got) != 1 || got[0] != "notify|tool-call:post" {
		t.Fatalf("space on action rows mutated state: %v", got)
	}
}

// Enter on a checkbox row toggles instead of delivering; Enter on an action
// row delivers once with the current selection and hides the overlay first.
func TestMultiConfirmCard_EnterSemantics(t *testing.T) {
	var gotID string
	var gotCancel bool
	doneCalls := 0
	c := NewMultiConfirmCard("t", "b", multiRows(), "", true, func(id string, cancelled bool) {
		gotID, gotCancel = id, cancelled
	})
	c.SetDone(func() { doneCalls++ })

	// Enter on a checkbox row toggles instead of delivering.
	c.HandleInput(KeyEnter)
	if gotID != "" || doneCalls != 0 {
		t.Fatalf("enter on toggle row delivered early: id=%q done=%d", gotID, doneCalls)
	}
	if len(c.SelectedIDs()) != 2 {
		t.Fatalf("enter-toggle failed: %v", c.SelectedIDs())
	}

	c.HandleInput("j") // → notify
	c.HandleInput("j") // → accept
	c.HandleInput(KeyEnter)
	if gotID != "accept" || gotCancel || doneCalls != 1 {
		t.Fatalf("action delivery wrong: id=%q cancel=%v done=%d", gotID, gotCancel, doneCalls)
	}
	sel := c.SelectedIDs()
	if len(sel) != 2 || sel[0] != "intercept|tool-call:pre" || sel[1] != "notify|tool-call:post" {
		t.Fatalf("selection order/content wrong: %v", sel)
	}
}

func TestMultiConfirmCard_EscapeCancelsWithoutSelection(t *testing.T) {
	var gotID string
	var gotCancel bool
	deliveries := 0
	c := newMultiCard(func(id string, cancelled bool) {
		gotID, gotCancel, deliveries = id, cancelled, deliveries+1
	})
	c.HandleInput(KeyEscape)
	if !gotCancel || gotID != "" || deliveries != 1 {
		t.Fatalf("esc must deliver one cancelled result, got id=%q cancel=%v n=%d", gotID, gotCancel, deliveries)
	}
}

// Single-select cards are untouched by the extension: space is consumed
// silently and Enter keeps delivering plain choices.
func TestConfirmCard_SingleSelectUnaffectedByMultiExtension(t *testing.T) {
	got := ""
	c := NewConfirmCard("t", "b", []ConfirmOption{{ID: "yes", Label: "Yes"}}, "", false, func(id string, cancelled bool) {
		got = id
	})
	c.HandleInput(" ")
	c.HandleInput(KeyEnter)
	if got != "yes" {
		t.Fatalf("single-select regression: got %q", got)
	}
}

func TestMultiConfirmCard_RenderShowsCheckboxesAndHint(t *testing.T) {
	c := newMultiCard(nil)
	lines := c.Render(60)
	joined := stripANSIJoin(lines)
	for _, want := range []string{"[ ] [intercept]", "[x] [notify]", "space toggle"} {
		if !strings.Contains(joined, want) {
			t.Errorf("render missing %q in:\n%s", want, joined)
		}
	}
}

func stripANSIJoin(lines []string) string {
	var b strings.Builder
	for _, l := range lines {
		b.WriteString(ansi.Strip(l))
		b.WriteString("\n")
	}
	return b.String()
}

// ShowConfirmMulti overlay round trip: defaults visible, space toggles, the
// action row delivers the checked selection, and the overlay hides.
func TestShowConfirmMulti_RoundTrip(t *testing.T) {
	engine := NewTUI(&fakeTerminal{w: 80, h: 24})
	editor := NewEditor()
	editor.SetTUI(engine)
	engine.SetFocus(editor)

	result, _ := engine.ShowConfirmMulti("Plugin hooks", "review", multiRows(), "", true)
	if len(engine.overlayStack) != 1 {
		t.Fatalf("overlay stack depth = %d, want 1", len(engine.overlayStack))
	}

	// Route keys through the same path the commandLoop uses: toggle the
	// intercept row on (cursor starts there), then accept.
	engine.handleKey(" ")
	engine.handleKey(KeyDown) // → notify row
	engine.handleKey(KeyDown) // → accept action
	engine.handleKey(KeyEnter)

	select {
	case got := <-result:
		if got.ActionID != "accept" || got.Cancelled {
			t.Fatalf("delivery = %+v, want accept", got)
		}
		want := []string{"intercept|tool-call:pre", "notify|tool-call:post"}
		if len(got.Selected) != 2 || got.Selected[0] != want[0] || got.Selected[1] != want[1] {
			t.Fatalf("selected = %v, want %v", got.Selected, want)
		}
	default:
		t.Fatal("result not delivered after enter")
	}
	if len(engine.overlayStack) != 0 {
		t.Fatalf("overlay not hidden after choice (depth=%d)", len(engine.overlayStack))
	}
	if engine.focus.Depth() != 1 {
		t.Fatalf("focus depth = %d, want 1 (editor restored)", engine.focus.Depth())
	}
}

// Dismissal through the TUI API reports Cancelled with no selection.
func TestShowConfirmMulti_EscDeliversCancelled(t *testing.T) {
	engine := NewTUI(&fakeTerminal{w: 80, h: 24})
	result, _ := engine.ShowConfirmMulti("Plugin hooks", "", multiRows(), "", true)
	engine.handleKey(KeyEscape)

	select {
	case got := <-result:
		if !got.Cancelled || got.ActionID != "" || len(got.Selected) != 0 {
			t.Fatalf("dismissal delivered %+v", got)
		}
	default:
		t.Fatal("dismissal not delivered")
	}
	if len(engine.overlayStack) != 0 {
		t.Fatal("overlay not hidden after dismissal")
	}
}
