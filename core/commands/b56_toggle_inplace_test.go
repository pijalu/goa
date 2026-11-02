// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"io"
	"sync"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/tui"
)

// b56Terminal is a minimal fake Terminal for driving the real engine.
type b56Terminal struct {
	mu     sync.Mutex
	w, h   int
	onIn   func(string)
	writes []string
}

func (f *b56Terminal) Start(onInput func(string), onResize func()) {
	f.mu.Lock()
	f.onIn = onInput
	f.mu.Unlock()
}
func (f *b56Terminal) Stop()                       {}
func (f *b56Terminal) Write(p []byte) (int, error) { return len(p), nil }
func (f *b56Terminal) WriteString(s string)        { _, _ = f.Write([]byte(s)) }
func (f *b56Terminal) Size() (int, int)            { return f.w, f.h }
func (f *b56Terminal) SetRaw() (func(), error)     { return func() {}, nil }
func (f *b56Terminal) HideCursor()                 {}
func (f *b56Terminal) ShowCursor()                 {}
func (f *b56Terminal) ClearScreen()                {}
func (f *b56Terminal) SetTitle(title string)       {}
func (f *b56Terminal) Read(p []byte) (int, error)  { return 0, io.EOF }
func (f *b56Terminal) Writes() []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]string, len(f.writes))
	copy(out, f.writes)
	return out
}

// b56Harness drives the real engine on a fake terminal with the production
// selector surfaces. Like the filmstrip harness (uiScenario), it is
// single-goroutine by design: the production delivery goroutine would run its
// engine.Apply inline here (no RunLoops) and race the test goroutine under
// -race, so deliveries are pumped deterministically via drain instead.
type b56Harness struct {
	engine *tui.TUI
	menu   *configMenu

	pending   []b56SelReq
	delivered []string // selector emits handed to the menu callback, in order
}

type b56SelReq struct {
	ch <-chan string
	cb func(string, bool)
}

// newB56Harness starts the engine and wires the sticky + in-place update
// surfaces exactly like the host, minus the async goroutine (see the
// single-goroutine note above).
func newB56Harness(t *testing.T, cfg *config.Config) *b56Harness {
	t.Helper()
	ctx := &core.Context{Config: cfg}
	term := &b56Terminal{w: 100, h: 30}
	engine := tui.NewTUI(term)
	if err := engine.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	t.Cleanup(engine.Stop)

	h := &b56Harness{engine: engine}
	ctx.SelectOptionStickyFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		h.pending = append(h.pending, b56SelReq{ch: engine.ShowSelectorSticky(title, options, current), cb: onSelected})
	}
	// B56: the in-place refresh surface, wired like the host (Apply →
	// ActiveSelector.UpdateItems) so the toggled row's description lands on
	// the live overlay.
	ctx.UpdateSelectorItemsFunc = func(items []tui.SelectorItem) {
		engine.Apply(func() {
			if sel := engine.ActiveSelector(); sel != nil {
				sel.UpdateItems(items)
			}
		})
	}
	h.menu = newConfigMenu(*ctx)
	return h
}

// drain delivers every emit that has already landed on the pending picker's
// channel, in order. Plain/sticky close emits ("") are delivered as ok=false
// — the cancel contract the menu's back() relies on.
func (h *b56Harness) drain() {
	for len(h.pending) > 0 {
		select {
		case v := <-h.pending[0].ch:
			req := h.pending[0]
			h.delivered = append(h.delivered, v)
			if req.cb != nil {
				req.cb(v, v != "")
			}
			// A sticky picker stays open until the close emit (""): keep the
			// entry queued so later toggles and the final close on the SAME
			// picker are drained too (mirrors deliverSelectorEmits).
			if v == "" {
				h.pending = h.pending[1:]
			}
		default:
			return
		}
	}
}

// deliveredCount reports how many emits reached the menu callback.
func (h *b56Harness) deliveredCount() int { return len(h.delivered) }

// TestB56_ToggleKeepsSelectorAndSelection drives the production Tools menu on
// a real engine: navigate to the second row, toggle it with Enter, and pin
// the B56 contract — the SAME selector overlay stays open with the SAME row
// highlighted and the flipped description refreshed in place. Escape then
// closes the picker and delivers the cancel contract.
func TestB56_ToggleKeepsSelectorAndSelection(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Enabled.SetEnabled("goal", true)
	cfg.Tools.Enabled.SetEnabled("webfetch", true)
	h := newB56Harness(t, cfg)

	h.menu.settingTools()
	h.engine.ApplySync(func() {}) // barrier: overlay shown on the loop
	first := h.engine.ActiveSelector()
	if first == nil {
		t.Fatal("no active selector after opening the Tools menu")
	}

	// Move to the SECOND row and toggle it.
	h.engine.SendKey("down")
	h.engine.ApplySync(func() {})
	second := h.engine.ActiveSelector()
	if second != first {
		t.Fatal("navigation replaced the selector overlay")
	}
	highlighted := second.SelectedValue()
	if highlighted == "" {
		t.Fatal("no highlighted row after navigation")
	}
	if highlighted == first.Items()[0].Value {
		t.Fatalf("test setup: expected the second row highlighted, got the first (%q)", highlighted)
	}

	assertInPlaceToggle(t, h, second, highlighted)
	assertEscapeCloses(t, h)
}

// assertInPlaceToggle confirms the highlighted row with Enter and pins the
// in-place contract: exactly one delivery, same selector instance, same
// selection, and the row's on/off description flipped on the live overlay.
func assertInPlaceToggle(t *testing.T, h *b56Harness, sel *tui.Selector, highlighted string) {
	t.Helper()
	want := "on"
	for _, it := range sel.Items() {
		if it.Value == highlighted && it.Description == "on" {
			want = "off"
		}
	}

	before := h.deliveredCount()
	h.engine.SendKey("enter")
	h.drain()

	if got := h.deliveredCount() - before; got != 1 {
		t.Fatalf("toggle delivered %d emits, want exactly 1", got)
	}
	if now := h.engine.ActiveSelector(); now != sel {
		t.Fatal("toggle rebuilt or closed the selector overlay instead of updating it in place")
	}
	if got := sel.SelectedValue(); got != highlighted {
		t.Fatalf("selection jumped from %q to %q after the toggle", highlighted, got)
	}
	for _, it := range sel.Items() {
		if it.Value == highlighted && it.Description != want {
			t.Fatalf("toggled row %q shows %q, want %q after the in-place refresh", highlighted, it.Description, want)
		}
	}
}

// assertEscapeCloses pins the cancel contract: Escape closes the sticky
// picker AND delivers the close emit ("", false) to the menu callback —
// that call is what runs the menu's back(); a delivery path that swallows it
// strands the user in a dead menu.
func assertEscapeCloses(t *testing.T, h *b56Harness) {
	t.Helper()
	before := h.deliveredCount()
	h.engine.SendKey("escape")
	h.drain()

	if got := h.deliveredCount() - before; got != 1 {
		t.Fatalf("close delivered %d emits, want exactly 1", got)
	}
	if last := h.delivered[len(h.delivered)-1]; last != "" {
		t.Fatalf("close emit delivered %q, want \"\"", last)
	}
	if h.engine.ActiveSelector() != nil {
		t.Fatal("Escape did not close the sticky picker")
	}
}
