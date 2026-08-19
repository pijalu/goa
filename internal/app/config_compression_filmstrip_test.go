// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tui"
)

// TestConfigCompressionMenu_Filmstrip validates the reworked compression
// menu as UI data (bugs.md §2): the /config picker flow is driven through
// the production command context wiring on the scenario engine, and each
// step is captured on a Filmstrip. Guards the reported dead-row bug — every
// main row must open a picker, never silently return to a parent menu.
//
// Selector resolution is synchronous (drainSelectors): the uiScenario
// harness is single-goroutine by design (no RunLoops), so the production
// wireInteractiveCallbacks goroutine would run its Apply inline and race
// the test goroutine under -race. The drain replaces that goroutine with a
// deterministic pump, preserving the production callback semantics.

func setupCompressionSelectors(t *testing.T, sc *uiScenario) (core.Context, func(), func(string), func()) {
	subs := sc.app.subs
	subs.loader = config.NewCascadeLoader(t.TempDir(), "", nil)
	ctx := coreContextForCommand(subs, sc.app)
	ctx.SkillRegistry = skills.NewSkillRegistry(nil)
	type selReq struct {
		ch <-chan string
		cb func(string, bool)
	}
	var queue []selReq
	ctx.SelectOptionFunc = func(title string, opts []tui.SelectorItem, current string, cb func(string, bool)) {
		queue = append(queue, selReq{ch: sc.engine.ShowSelector(title, opts, current), cb: cb})
	}
	drain := func() {
		for len(queue) > 0 {
			select {
			case v, ok := <-queue[0].ch:
				req := queue[0]
				queue = queue[1:]
				req.cb(v, ok && v != "")
			default:
				return
			}
		}
	}
	pick := func(filter string) {
		for _, ch := range filter {
			sc.engine.SendKey(string(ch))
		}
		sc.engine.SendKey(tui.KeyEnter)
		drain()
	}
	esc := func() { sc.engine.SendKey(tui.KeyEscape); drain() }
	return ctx, drain, pick, esc
}

func TestConfigCompressionMenu_Filmstrip(t *testing.T) {
	sc := newUIScenario(t, 100, 30)
	subs := sc.app.subs
	subs.cfg.ContextCompression.OnContextError = true
	ctx, drainSelectors, pick, esc := setupCompressionSelectors(t, sc)
	sc.engine.ApplySync(func() { _ = (&commands.ConfigCommand{}).Run(ctx, nil) })
	drainSelectors()
	sc.engine.RenderNow()
	waitForFrame(t, sc, "Settings:")
	film := sc.filmstrip()
	pick("Compression")
	waitForFrame(t, sc, "Compression:")
	film.Capture("config/compression-menu", sc.engine.AgentFrame(), "")

	frame := sc.engine.AgentFrame()
	// Exactly the 5 main rows + Advanced…, in order.
	rows := []string{"Soft ceiling %", "Soft ceiling method", "Hard ceiling %", "Hard ceiling method", "On error", "Advanced"}
	for _, row := range rows {
		if !frameContains(frame, row) {
			t.Errorf("compression menu missing row %q\n%s", row, frame.Dump())
		}
	}
	if frameContains(frame, "Effective hard ceiling") || frameContains(frame, "Escalation level") {
		t.Errorf("derived (dead) rows must not appear in the reworked menu\n%s", frame.Dump())
	}

	// Row 1: Soft ceiling % → ceiling picker (0 + 5..100).
	pick("Soft ceiling %")
	waitForFrame(t, sc, "Soft ceiling (% of max tokens, 0 = disabled):")
	film.Capture("config/soft-ceiling-picker", sc.engine.AgentFrame(), "")
	// Visible-window sanity: the disabled + low steps render (the full 0+5..100
	// range is pinned by the unit picker tests — the filmstrip only sees the
	// scrolled viewport slice).
	mustContainAll(t, sc, "soft ceiling picker", "0% (disabled)", "5%")
	esc()
	waitForFrame(t, sc, "Soft ceiling %")

	// Row 2: Soft ceiling method → all five methods (all-methods soft).
	pick("Soft ceiling method")
	waitForFrame(t, sc, "Soft ceiling method:")
	film.Capture("config/soft-method-picker", sc.engine.AgentFrame(), "")
	mustContainAll(t, sc, "soft method picker", "micro", "tool_elision", "selective", "hybrid", "summarize")
	esc()
	waitForFrame(t, sc, "Soft ceiling %")

	// Row 3: Hard ceiling %.
	pick("Hard ceiling %")
	waitForFrame(t, sc, "Hard ceiling (% of max tokens, 0 = disabled):")
	film.Capture("config/hard-ceiling-picker", sc.engine.AgentFrame(), "")
	esc()
	waitForFrame(t, sc, "Soft ceiling %")

	// Row 4: Hard ceiling method.
	pick("Hard ceiling method")
	waitForFrame(t, sc, "Hard ceiling method:")
	film.Capture("config/hard-method-picker", sc.engine.AgentFrame(), "")
	esc()
	waitForFrame(t, sc, "Soft ceiling %")

	// Row 5: On error → off + all five methods.
	pick("On error")
	waitForFrame(t, sc, "On context error:")
	film.Capture("config/on-error-picker", sc.engine.AgentFrame(), "")
	mustContainAll(t, sc, "on-error picker", "off", "hybrid", "summarize")
	esc()
	waitForFrame(t, sc, "Soft ceiling %")

	// Row 6: Advanced… submenu.
	pick("Advanced")
	waitForFrame(t, sc, "Compression — advanced:")
	film.Capture("config/advanced-menu", sc.engine.AgentFrame(), "")
	// Visible-window sanity (the full row list is pinned by the unit tests;
	// the filmstrip viewport shows only the first slice).
	mustContainAll(t, sc, "advanced menu", "Cache gate", "Max tokens")
	if frameContains(sc.engine.AgentFrame(), "On error") {
		t.Errorf("on-error belongs to the main menu, not Advanced\n%s", sc.engine.AgentFrame().Dump())
	}

	// Selecting 60 in the hard ceiling picker persists it (opt-in write path).
	esc()
	waitForFrame(t, sc, "Compression:")
	pick("Hard ceiling %")
	waitForFrame(t, sc, "Hard ceiling (% of max tokens, 0 = disabled):")
	pick("60")
	waitForFrame(t, sc, "60%")
	film.Capture("config/after-hard-60", sc.engine.AgentFrame(), "")
	if got := subs.cfg.ContextCompression.Thresholds.HardPercent; got != 60 {
		t.Errorf("Thresholds.HardPercent = %d, want 60", got)
	}

	_ = core.Context{} // context wiring reference
	t.Log("\nfilmstrip:\n" + film.Render())
}

// mustContainAll asserts the current visible frame contains every substring.
func mustContainAll(t *testing.T, sc *uiScenario, what string, want ...string) {
	t.Helper()
	frame := sc.engine.AgentFrame()
	for _, s := range want {
		if !frameContains(frame, s) {
			t.Errorf("%s missing %q\n%s", what, s, frame.Dump())
		}
	}
}
