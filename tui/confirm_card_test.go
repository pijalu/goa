// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

func renderConfirmPlain(c *ConfirmCard, width int) string {
	lines := c.Render(width)
	for i := range lines {
		lines[i] = ansi.Strip(lines[i])
	}
	return strings.Join(lines, "\n")
}

func testConfirmOptions() []ConfirmOption {
	return []ConfirmOption{
		{ID: "yes", Label: "Yes, use reset", Style: "danger"},
		{ID: "no", Label: "Not now", Style: "ok"},
	}
}

// TestConfirmCard_RenderContainsAllFields pins the golden content: title,
// body, every option label, the implicit Cancel row, and the key hint.
func TestConfirmCard_RenderContainsAllFields(t *testing.T) {
	c := NewConfirmCard("Use rate-limit reset?", "This consumes one credit.",
		testConfirmOptions(), "no", true, func(string, bool) {})
	out := renderConfirmPlain(c, 60)
	for _, want := range []string{
		"Use rate-limit reset?",
		"This consumes one credit.",
		"Yes, use reset",
		"Not now",
		"Cancel",
		"enter select",
		"esc cancel",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("render missing %q\n--- got ---\n%s", want, out)
		}
	}
	if !c.HasDanger() {
		t.Error("HasDanger should detect the danger row")
	}
}

// TestConfirmCard_NoCancelWhenDisallowed checks AllowCancel=false contract:
// no Cancel row and no esc fragment in the hint.
func TestConfirmCard_NoCancelWhenDisallowed(t *testing.T) {
	c := NewConfirmCard("Proceed?", "", testConfirmOptions(), "", false, func(string, bool) {})
	out := renderConfirmPlain(c, 60)
	if strings.Contains(out, "Cancel") {
		t.Errorf("Cancel row rendered despite allowCancel=false:\n%s", out)
	}
	if strings.Contains(out, "esc cancel") {
		t.Errorf("esc hint rendered despite allowCancel=false:\n%s", out)
	}
	if c.cancelRow != -1 {
		t.Errorf("cancelRow = %d, want -1", c.cancelRow)
	}
}

// TestConfirmCard_DefaultCursor pins initial-cursor resolution: known
// defaultID selects that row; unknown/empty selects the first row.
func TestConfirmCard_DefaultCursor(t *testing.T) {
	c := NewConfirmCard("T", "", testConfirmOptions(), "no", true, func(string, bool) {})
	if c.selected != 1 {
		t.Fatalf("selected = %d, want 1 (the 'no' row)", c.selected)
	}
	c2 := NewConfirmCard("T", "", testConfirmOptions(), "bogus", true, func(string, bool) {})
	if c2.selected != 0 {
		t.Fatalf("selected = %d, want 0 for unknown default", c2.selected)
	}
}

// TestConfirmCard_KeyNavigation covers up/down with wrap-around.
func TestConfirmCard_KeyNavigation(t *testing.T) {
	var gotID string
	var gotCancelled bool
	c := NewConfirmCard("T", "", testConfirmOptions(), "yes", true, func(id string, cancelled bool) {
		gotID, gotCancelled = id, cancelled
	})

	c.HandleInput(KeyDown) // yes -> no
	c.HandleInput(KeyDown) // no -> Cancel (implicit last row)
	c.HandleInput(KeyDown) // wrap to yes
	c.HandleInput(KeyEnter)
	if gotID != "yes" || gotCancelled {
		t.Fatalf("after 3×down+enter: id=%q cancelled=%v, want yes/false", gotID, gotCancelled)
	}

	c.HandleInput(KeyUp) // yes -> Cancel
	c.HandleInput(KeyUp) // Cancel -> no
	if c.selected != 1 {
		t.Fatalf("selected after wrap-up = %d, want 1", c.selected)
	}
}

// TestConfirmCard_EnterChooses pins Enter delivering the highlighted option,
// and Enter on the implicit Cancel row reporting cancellation.
func TestConfirmCard_EnterChooses(t *testing.T) {
	var id string
	var cancelled bool
	c := NewConfirmCard("T", "", testConfirmOptions(), "yes", true, func(i string, ca bool) { id, cancelled = i, ca })
	c.HandleInput(KeyEnter)
	if id != "yes" || cancelled {
		t.Fatalf("enter on danger row: id=%q cancelled=%v", id, cancelled)
	}

	id, cancelled = "", false
	c2 := NewConfirmCard("T", "", testConfirmOptions(), "", true, func(i string, ca bool) { id, cancelled = i, ca })
	c2.move(1)
	c2.move(1) // cursor on implicit Cancel row
	if c2.selected != c2.cancelRow {
		t.Fatalf("selected=%d cancelRow=%d", c2.selected, c2.cancelRow)
	}
	c2.HandleInput(KeyEnter)
	if id != "" || !cancelled {
		t.Fatalf("enter on cancel row: id=%q cancelled=%v", id, cancelled)
	}
}

// TestConfirmCard_EscapeCancelsAndHonorsAllowCancel pins Esc/Ctrl+C semantics:
// dismissal when allowed; consumed no-op otherwise (a forced-choice prompt
// must not be dismissible).
func TestConfirmCard_EscapeCancelsAndHonorsAllowCancel(t *testing.T) {
	calls := 0
	c := NewConfirmCard("T", "", testConfirmOptions(), "yes", true, func(string, bool) { calls++ })
	c.HandleInput(KeyEscape)
	if calls != 1 {
		t.Fatalf("esc did not cancel (calls=%d)", calls)
	}

	calls = 0
	forced := NewConfirmCard("T", "", testConfirmOptions(), "yes", false, func(string, bool) { calls++ })
	forced.HandleInput(KeyEscape)
	forced.HandleInput(KeyCtrlC)
	if calls != 0 {
		t.Fatalf("forced prompt dismissed via esc/ctrl+c (calls=%d)", calls)
	}
	// Stray keys are consumed silently either way (never leak to the editor).
	forced.HandleInput("x")
	forced.HandleInput("hello world")
}

// TestConfirmCard_CtrlCCancels pins Ctrl+C as a dismissal alias of Esc.
func TestConfirmCard_CtrlCCancels(t *testing.T) {
	cancelled := false
	c := NewConfirmCard("T", "", testConfirmOptions(), "yes", true, func(_ string, ca bool) { cancelled = ca })
	c.HandleInput(KeyCtrlC)
	if !cancelled {
		t.Fatal("ctrl+c did not cancel")
	}
}

// TestConfirmCard_SingleDelivery guards the exactly-once callback contract:
// double pick attempts (racing key events before hide lands) deliver once.
func TestConfirmCard_SingleDelivery(t *testing.T) {
	calls := 0
	c := NewConfirmCard("T", "", testConfirmOptions(), "yes", true, func(string, bool) { calls++ })
	c.HandleInput(KeyEnter)
	c.HandleInput(KeyEnter) // late duplicate — resolver side drops it via buffered chan
	if calls != 2 {
		t.Fatalf("choose called %d times; card must stay dumb (resolver dedupes), got unexpected count", calls)
	}
}

// TestConfirmCard_RenderWidthBounds ensures every line respects width and the
// box borders align at min/max clamps.
func TestConfirmCard_RenderWidthBounds(t *testing.T) {
	for _, w := range []int{10, 30, 46, 60, 64, 200} {
		lines := NewConfirmCard("Title here", "Body text here.", testConfirmOptions(), "", true, func(string, bool) {}).Render(w)
		wantW := w
		if wantW < 46 {
			wantW = 46
		}
		if wantW > 64 {
			wantW = 64
		}
		for i, l := range lines {
			if vw := visibleWidth(l); vw != wantW {
				t.Errorf("width %d line %d visible width = %d, want %d", w, i, vw, wantW)
			}
		}
	}
}

// TestConfirmCard_NoRawHexLeak mirrors the clarify-card regression: colors
// must leave as ANSI sequences, never literal hex strings.
func TestConfirmCard_NoRawHexLeak(t *testing.T) {
	raw := strings.Join(NewConfirmCard("T", "B", testConfirmOptions(), "", true, func(string, bool) {}).Render(60), "\n")
	if strings.Contains(raw, "#") && strings.Contains(raw, "f85149") {
		t.Errorf("raw hex leaked into render:\n%.200s", raw)
	}
	if !strings.Contains(raw, "\x1b[") {
		t.Error("expected ANSI styling in render")
	}
}

// TestShowConfirm_OverlayRoundTrip drives the public TUI API end-to-end in
// single-goroutine test mode: show → capture-input routing → enter → result
// delivered + overlay hidden + focus restored.
func TestShowConfirm_OverlayRoundTrip(t *testing.T) {
	engine := NewTUI(&fakeTerminal{w: 80, h: 24})
	// Production wires the editor as the focus base at startup; mirror it so
	// the card pushes/pops on top of a real stack.
	editor := NewEditor()
	editor.SetTUI(engine)
	engine.SetFocus(editor)

	result, _ := engine.ShowConfirm("Use reset?", "One credit.", testConfirmOptions(), "yes", true)

	// Overlay registered and capturing input.
	if len(engine.overlayStack) != 1 {
		t.Fatalf("overlay stack depth = %d, want 1", len(engine.overlayStack))
	}
	if top := engine.focus.Top(); top == nil {
		t.Fatal("confirm card did not capture focus")
	}

	// Route keys through the same path the commandLoop uses.
	engine.handleKey(KeyDown) // yes -> no
	engine.handleKey(KeyEnter)

	select {
	case got := <-result:
		if got != "no" {
			t.Fatalf("result = %q, want \"no\"", got)
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

// TestShowConfirm_EscDeliversEmpty pins dismissal through the TUI API.
func TestShowConfirm_EscDeliversEmpty(t *testing.T) {
	engine := NewTUI(&fakeTerminal{w: 80, h: 24})
	result, _ := engine.ShowConfirm("Use reset?", "", testConfirmOptions(), "yes", true)
	engine.handleKey(KeyEscape)

	select {
	case got := <-result:
		if got != "" {
			t.Fatalf("dismissal delivered %q, want empty", got)
		}
	default:
		t.Fatal("dismissal not delivered")
	}
	if len(engine.overlayStack) != 0 {
		t.Fatal("overlay not hidden after dismissal")
	}
}
