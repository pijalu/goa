// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"path/filepath"
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
	t.Setenv("GOA_HOME", t.TempDir())
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
	// B56: sticky pickers (toggle menus) go through SelectOptionStickyFunc in
	// production — replace the production delivery goroutine with the same
	// deterministic pump (single-goroutine harness; see header comment).
	ctx.SelectOptionStickyFunc = func(title string, opts []tui.SelectorItem, current string, cb func(string, bool)) {
		queue = append(queue, selReq{ch: sc.engine.ShowSelectorSticky(title, opts, current), cb: cb})
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

func TestConfigCompressionIdleSummarize_Filmstrip(t *testing.T) {
	sc := newUIScenario(t, 120, 60)
	subs := sc.app.subs
	ctx, drainSelectors, pick, _ := setupCompressionSelectors(t, sc)
	sc.engine.ApplySync(func() { _ = (&commands.ConfigCommand{}).Run(ctx, nil) })
	drainSelectors()
	sc.engine.RenderNow()
	waitForFrame(t, sc, "Settings:")
	film := sc.filmstrip()
	pick("Compression")
	waitForFrame(t, sc, "Compression:")
	pick("Advanced")
	waitForFrame(t, sc, "Compression — advanced:")
	mustContainAll(t, sc, "idle summarize advanced rows",
		"Idle summarize: minimum context %", "10%",
		"Idle summarize: delay seconds", "240s")
	film.Capture("config/idle-summarize-defaults", sc.engine.AgentFrame(), "")

	pick("Idle summarize: minimum")
	waitForFrame(t, sc, "Idle summarize minimum context occupancy:")
	film.Capture("config/idle-summarize-trigger-picker", sc.engine.AgentFrame(), "")
	pick("25")
	waitForFrame(t, sc, "Compression — advanced:")
	if got := subs.cfg.ContextCompression.IdleSummarize.TriggerPercentValue(); got != 25 {
		t.Fatalf("idle trigger after filmstrip selection = %d, want 25", got)
	}
	mustContainAll(t, sc, "updated idle trigger row", "25%")

	pick("Idle summarize: delay")
	waitForFrame(t, sc, "Idle summarize delay (seconds; 0 = disabled):")
	film.Capture("config/idle-summarize-delay-picker", sc.engine.AgentFrame(), "")
	pick("off")
	waitForFrame(t, sc, "Compression — advanced:")
	if got := subs.cfg.ContextCompression.IdleSummarize.DelaySecondsValue(); got != 0 {
		t.Fatalf("idle delay after filmstrip selection = %d, want explicit zero", got)
	}
	mustContainAll(t, sc, "updated idle delay row", "off")
	assertIdleSummarizePersistence(t, 25, 0)
	film.Capture("config/idle-summarize-updated", sc.engine.AgentFrame(), "")
	t.Log("\nfilmstrip:\n" + film.Render())
}

func TestConfigCompressionPerModelIdleSummarize_Filmstrip(t *testing.T) {
	sc := newUIScenario(t, 140, 70)
	subs := sc.app.subs
	subs.cfg.Models = append(subs.cfg.Models, config.ModelConfig{ID: "qwen", ProviderID: "local", Model: "qwen-wire"})
	ctx, drainSelectors, pick, _ := setupCompressionSelectors(t, sc)
	wantTrigger, wantDelay := 25, 0
	if err := ctx.ConfigSaver.SaveHomeProvidersAndModels(subs.cfg); err != nil {
		t.Fatalf("SaveHomeProvidersAndModels: %v", err)
	}
	sc.engine.ApplySync(func() { _ = (&commands.ConfigCommand{}).Run(ctx, nil) })
	drainSelectors()
	sc.engine.RenderNow()
	waitForFrame(t, sc, "Settings:")
	film := sc.filmstrip()
	pick("Compression")
	waitForFrame(t, sc, "Compression:")
	pick("Advanced")
	waitForFrame(t, sc, "Compression — advanced:")
	pick("Per-model overrides")
	waitForFrame(t, sc, "Per-model compression overrides")
	pick("qwen")
	waitForFrame(t, sc, "Compression overrides for qwen:")
	mustContainAll(t, sc, "per-model idle defaults", "Idle summarize: minimum context %", "Idle summarize: delay seconds")
	film.Capture("config/per-model-idle-inherited", sc.engine.AgentFrame(), "")

	pick("Idle summarize: minimum")
	waitForFrame(t, sc, "Idle summarize minimum context occupancy for qwen:")
	mustContainAll(t, sc, "per-model idle trigger picker", "inherit (clear)", "0%")
	film.Capture("config/per-model-idle-trigger-picker", sc.engine.AgentFrame(), "")
	pick("25")
	waitForFrame(t, sc, "Compression overrides for qwen:")
	assertOptionalIntValue(t, "per-model trigger after selection", subs.cfg.ContextCompression.PerModel["qwen"].IdleSummarize.TriggerPercent, &wantTrigger)

	pick("Idle summarize: delay")
	waitForFrame(t, sc, "Idle summarize delay for qwen")
	mustContainAll(t, sc, "per-model idle delay picker", "inherit (clear)", "off", "240s")
	film.Capture("config/per-model-idle-delay-picker", sc.engine.AgentFrame(), "")
	pick("off")
	waitForFrame(t, sc, "Compression overrides for qwen:")
	ov := subs.cfg.ContextCompression.PerModel["qwen"].IdleSummarize
	assertOptionalIntValue(t, "per-model delay after selection", ov.DelaySeconds, &wantDelay)
	mustContainAll(t, sc, "per-model idle updated rows", "25%", "off")
	film.Capture("config/per-model-idle-updated", sc.engine.AgentFrame(), "")
	reloaded := reloadCompressionFilmstripConfig(t)
	ov = reloaded.ContextCompression.PerModel["qwen"].IdleSummarize
	assertOptionalIntValue(t, "reloaded per-model trigger", ov.TriggerPercent, &wantTrigger)
	assertOptionalIntValue(t, "reloaded per-model delay", ov.DelaySeconds, &wantDelay)

	// The non-empty inherit action must reach the setter as an empty clear;
	// a raw empty selector result is reserved for Escape/cancellation.
	pick("Idle summarize: minimum")
	waitForFrame(t, sc, "Idle summarize minimum context occupancy for qwen:")
	pick("inherit (clear)")
	waitForFrame(t, sc, "Compression overrides for qwen:")
	ov = subs.cfg.ContextCompression.PerModel["qwen"].IdleSummarize
	assertOptionalIntValue(t, "trigger after inherit selection", ov.TriggerPercent, nil)
	assertOptionalIntValue(t, "delay after clearing trigger", ov.DelaySeconds, &wantDelay)
	reloaded = reloadCompressionFilmstripConfig(t)
	ov = reloaded.ContextCompression.PerModel["qwen"].IdleSummarize
	assertOptionalIntValue(t, "reloaded cleared trigger", ov.TriggerPercent, nil)
	assertOptionalIntValue(t, "reloaded independent delay", ov.DelaySeconds, &wantDelay)
	film.Capture("config/per-model-idle-trigger-inherited", sc.engine.AgentFrame(), "")
	t.Log("\nfilmstrip:\n" + film.Render())
}

func assertOptionalIntValue(t *testing.T, name string, got, want *int) {
	t.Helper()
	if want == nil {
		if got != nil {
			t.Fatalf("%s = %d, want nil (inherit)", name, *got)
		}
		return
	}
	if got == nil || *got != *want {
		t.Fatalf("%s = %v, want %d", name, got, *want)
	}
}

func reloadCompressionFilmstripConfig(t *testing.T) *config.Config {
	t.Helper()
	loader := config.NewCascadeLoader(t.TempDir(), "", nil)
	reloaded, err := loader.Load()
	if err != nil {
		t.Fatalf("reload saved filmstrip config: %v", err)
	}
	return reloaded
}

func assertIdleSummarizePersistence(t *testing.T, trigger, delay int) {
	t.Helper()
	path := filepath.Join(os.Getenv("GOA_HOME"), ".goa", "config.yaml")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("idle summarize selections were not persisted: %v", err)
	}
	loader := config.NewCascadeLoader(t.TempDir(), "", nil)
	reloaded, err := loader.Load()
	if err != nil {
		t.Fatalf("reload saved filmstrip config: %v\n%s", err, data)
	}
	if got := reloaded.ContextCompression.IdleSummarize.TriggerPercentValue(); got != trigger {
		t.Errorf("reloaded idle trigger = %d, want %d\n%s", got, trigger, data)
	}
	if got := reloaded.ContextCompression.IdleSummarize.DelaySecondsValue(); got != delay {
		t.Errorf("reloaded idle delay = %d, want %d\n%s", got, delay, data)
	}
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
