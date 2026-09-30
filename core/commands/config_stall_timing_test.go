// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	internal "github.com/pijalu/goa/internal"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/provider"
)

// stallTimingContext builds a menu context wired to a REAL provider manager
// (so BuildStreamOptions reflects the live config) plus a running session, the
// same shape the app uses, with the shipped stall-timing defaults.
func stallTimingContext(t *testing.T) (*core.Context, *selectRecorder, *inputRecorder) {
	t.Helper()
	ctx, sr, ir := stallTimingContextWithHomeConfig(t, "")
	if ctx.Config.Execution.ActivityTimeout != "5m" || ctx.Config.Execution.ActivityWarnAfter != "3m20s" {
		t.Fatalf("precondition: shipped stall timing = (%q, %q), want (5m, 3m20s)",
			ctx.Config.Execution.ActivityTimeout, ctx.Config.Execution.ActivityWarnAfter)
	}
	return ctx, sr, ir
}

// stallTimingContextWithHomeConfig seeds $HOME/.goa/config.yaml with the given
// body BEFORE any loader or session exists, so the whole stack (config, live
// stream options, saver) starts from that home layer.
func stallTimingContextWithHomeConfig(t *testing.T, homeYAML string) (*core.Context, *selectRecorder, *inputRecorder) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	if homeYAML != "" {
		dir := filepath.Join(home, ".goa")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatalf("seed home config dir: %v", err)
		}
		if err := os.WriteFile(filepath.Join(dir, "config.yaml"), []byte(homeYAML), 0o644); err != nil {
			t.Fatalf("seed home config: %v", err)
		}
	}

	loader := config.NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	ctx, sr, ir, events := newMenuTestContext(t, cfg)
	ctx.ConfigSaver = loader
	pm := provider.NewProviderManager(cfg)
	ctx.ProviderManager = pm
	ctx.AgentManager = core.NewAgentManager(
		cfg,
		nil,
		core.NewLoopDetector(core.DefaultLoopDetectorConfig()),
		core.NewSessionState(internal.ModeState{Major: internal.MajorCoder}),
		events,
		"",
	)
	if _, err := ctx.AgentManager.StartSession(
		agenticprovider.Model{ID: "test-model", Api: agenticprovider.ApiOpenAICompletions},
		pm.BuildStreamOptions(), "sys", nil, cfg,
	); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	return ctx, sr, ir
}

func liveStreamOptions(t *testing.T, ctx *core.Context) agenticprovider.StreamOptions {
	t.Helper()
	agent := ctx.AgentManager.CurrentAgent()
	if agent == nil {
		t.Fatal("no active agent")
	}
	return agent.StreamOptions()
}

// TestConfigSet_ActivityWarnAfterAppliesAndPersists: the stall warning lead must
// be settable from /config:set AND reach the RUNNING session — stream options
// are sampled once at session start, so a value that only lands in the config
// file would silently wait for the next restart ("toggle that does nothing").
func TestConfigSet_ActivityWarnAfterAppliesAndPersists(t *testing.T) {
	ctx, _, _ := stallTimingContext(t)

	// Shipped defaults already reach the live session: the 5m byte budget and
	// the 3m20s warning lead.
	live := liveStreamOptions(t, ctx)
	if live.ActivityWarnAfter != 200*time.Second || live.IdleTimeout != 5*time.Minute {
		t.Fatalf("live options = warn %s / window %s, want 3m20s / 5m", live.ActivityWarnAfter, live.IdleTimeout)
	}

	if err := applyConfigSet(*ctx, "execution.activity_warn_after", "20s"); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}

	if got := ctx.Config.Execution.ActivityWarnAfter; got != "20s" {
		t.Errorf("config activity_warn_after = %q, want 20s", got)
	}
	reloaded, err := ctx.ConfigSaver.(*config.CascadeLoader).Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Execution.ActivityWarnAfter != "20s" {
		t.Errorf("persisted activity_warn_after = %q, want 20s", reloaded.Execution.ActivityWarnAfter)
	}
	if got := liveStreamOptions(t, ctx).ActivityWarnAfter; got != 20*time.Second {
		t.Errorf("running session warn lead = %s, want 20s (must apply without restart)", got)
	}

	// An unparseable value is refused and changes nothing.
	if err := applyConfigSet(*ctx, "execution.activity_warn_after", "soon"); err != nil {
		t.Fatalf("applyConfigSet(invalid): %v", err)
	}
	if got := ctx.Config.Execution.ActivityWarnAfter; got != "20s" {
		t.Errorf("invalid value changed the config: %q", got)
	}
	if got := liveStreamOptions(t, ctx).ActivityWarnAfter; got != 20*time.Second {
		t.Errorf("invalid value changed the live session: %s", got)
	}
}

// TestConfigSet_ActivityTimeoutPushesLiveOptions: the retry window itself must
// also be settable from /config and pushed into the running session, so the
// user can retune the shipped 5m/3m20s pair live.
func TestConfigSet_ActivityTimeoutPushesLiveOptions(t *testing.T) {
	ctx, _, _ := stallTimingContext(t)

	// Start from a lead that still fits the new 60s window (its event stall is
	// 45s), so the window change is the only thing under test.
	if err := applyConfigSet(*ctx, "execution.activity_warn_after", "20s"); err != nil {
		t.Fatalf("applyConfigSet(warn): %v", err)
	}
	if err := applyConfigSet(*ctx, "execution.activity_timeout", "60s"); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}
	if got := ctx.Config.Execution.ActivityTimeout; got != "60s" {
		t.Errorf("config activity_timeout = %q, want 60s", got)
	}
	reloaded, err := ctx.ConfigSaver.(*config.CascadeLoader).Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Execution.ActivityTimeout != "60s" {
		t.Errorf("persisted activity_timeout = %q, want 60s", reloaded.Execution.ActivityTimeout)
	}
	live := liveStreamOptions(t, ctx)
	if live.IdleTimeout != 60*time.Second {
		t.Errorf("running session window = %s, want 60s (must apply without restart)", live.IdleTimeout)
	}
	if live.ActivityWarnAfter != 20*time.Second {
		t.Errorf("warning lead = %s, want the configured 20s (window change must not disturb it)", live.ActivityWarnAfter)
	}
}

// TestRetrySettingsMenu_ShowsStallTiming: both values must be VISIBLE (with the
// values actually in effect) and EDITABLE from /config → Retry settings.
func TestRetrySettingsMenu_ShowsStallTiming(t *testing.T) {
	ctx, sr, ir := stallTimingContext(t)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("retry", true)

	descriptions := map[string]string{}
	for _, item := range sr.options {
		descriptions[item.Value] = item.Description
	}
	windowDesc, ok := descriptions["stall_timeout"]
	if !ok {
		t.Fatalf("retry settings menu is missing the retry-window entry: %+v", sr.options)
	}
	// The window shown is the EVENT stall the agent retries on (3/4 of the 5m
	// byte budget = 225s), with the configured lead inside it.
	if windowDesc != "225 (warn at 200)" {
		t.Errorf("retry-window description = %q, want \"225 (warn at 200)\"", windowDesc)
	}
	warnDesc, ok := descriptions["stall_warn"]
	if !ok {
		t.Fatalf("retry settings menu is missing the stall-warning entry: %+v", sr.options)
	}
	if warnDesc != "200" {
		t.Errorf("stall-warning description = %q, want \"200\"", warnDesc)
	}

	// Editing the warning lead from the menu persists it and re-renders with the
	// new value.
	sr.onSel("stall_warn", true)
	if ir.prompt == "" {
		t.Fatal("selecting the stall-warning entry must prompt for a duration")
	}
	ir.onSub("20s", true)

	if got := ctx.Config.Execution.ActivityWarnAfter; got != "20s" {
		t.Errorf("menu toggle set activity_warn_after = %q, want 20s", got)
	}
	var refreshed string
	for _, item := range sr.options {
		if item.Value == "stall_warn" {
			refreshed = item.Description
		}
	}
	if refreshed != "20" {
		t.Errorf("menu did not re-render the new lead as plain seconds, got %q", refreshed)
	}
}

// TestRetrySettingsMenu_DerivesWarningWhenUnset: with no explicit lead the menu
// must show the value the agent would derive (two thirds of the EVENT stall)
// rather than an empty field.
func TestRetrySettingsMenu_DerivesWarningWhenUnset(t *testing.T) {
	ctx, sr, _ := stallTimingContext(t)
	ctx.Config.Execution.ActivityWarnAfter = ""

	menu := newConfigMenu(*ctx)
	menu.settingRetrySettings()

	var warnDesc, windowDesc string
	for _, item := range sr.options {
		switch item.Value {
		case "stall_warn":
			warnDesc = item.Description
		case "stall_timeout":
			windowDesc = item.Description
		}
	}
	if windowDesc != "225 (warn at 150)" {
		t.Errorf("derived window description = %q, want \"225 (warn at 150)\"", windowDesc)
	}
	if warnDesc != "150 (derived: 2/3 of 225)" {
		t.Errorf("derived warning description = %q, want \"150 (derived: 2/3 of 225)\"", warnDesc)
	}
}

// TestStallTiming_PlainSecondsPrimitives pins the BUG-5 helpers: the UI
// speaks plain seconds — formatStallSeconds renders whole seconds as bare
// numbers, parseStallDuration accepts a bare number as seconds alongside
// explicit durations (which pass through with the unit the user chose).
func TestStallTiming_PlainSecondsPrimitives(t *testing.T) {
	parseTests := []struct {
		in      string
		want    string
		wantErr bool
	}{
		{in: "60", want: "60s"},
		{in: "60s", want: "60s"},
		{in: "2m", want: "2m"},
		{in: "1m30s", want: "1m30s"},
		{in: " 45 ", want: "45s"},
		{in: "0", wantErr: true},
		{in: "-5", wantErr: true},
		{in: "soon", wantErr: true},
		{in: "", wantErr: true},
	}
	for _, tt := range parseTests {
		got, err := parseStallDuration(tt.in)
		if tt.wantErr != (err != nil) {
			t.Errorf("parseStallDuration(%q) error = %v, wantErr %v", tt.in, err, tt.wantErr)
			continue
		}
		if !tt.wantErr && got != tt.want {
			t.Errorf("parseStallDuration(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}

	formatTests := []struct {
		in   time.Duration
		want string
	}{
		{in: 45 * time.Second, want: "45"},
		{in: 2 * time.Minute, want: "120"},
		{in: 1500 * time.Millisecond, want: "1.5s"},
	}
	for _, tt := range formatTests {
		if got := formatStallSeconds(tt.in); got != tt.want {
			t.Errorf("formatStallSeconds(%s) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestRetrySettingsMenu_PrefillsPlainSeconds: the stall-timing input prompts
// prefill whole-second values as bare numbers ("45", not "45s") so editing
// stays in the unit the user reads (bugs.md BUG-5), and a bare-number answer
// is accepted and persisted canonically ("60" → "60s").
func TestRetrySettingsMenu_PrefillsPlainSeconds(t *testing.T) {
	ctx, sr, ir := stallTimingContext(t)

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("retry", true)
	sr.onSel("stall_timeout", true)
	if ir.current != "300" {
		t.Errorf("window prompt prefill = %q, want \"300\" (5m in plain seconds)", ir.current)
	}
	ir.onSub("60", true)
	if got := ctx.Config.Execution.ActivityTimeout; got != "60s" {
		t.Errorf("bare-number input persisted as %q, want \"60s\"", got)
	}

	sr.onSel("stall_timeout", true)
	if ir.current != "60" {
		t.Errorf("window prompt re-prefill = %q, want \"60\"", ir.current)
	}

	sr.onSel("stall_warn", true)
	// The shipped 3m20s lead no longer fits the 60s window (its event stall is
	// 45s), so the prompt offers the value the agent would derive: 2/3 of 45s.
	if ir.current != "30" {
		t.Errorf("warning prompt prefill = %q, want \"30\" (derived 2/3 of the 45s event stall)", ir.current)
	}
}

// TestConfigKeyCompletions_StallTiming pins discoverability of both keys for
// /config:set.
func TestConfigKeyCompletions_StallTiming(t *testing.T) {
	for _, key := range []string{"execution.activity_timeout", "execution.activity_warn_after"} {
		var found bool
		for _, comp := range configKeyCompletions(key) {
			if comp.Value == key {
				found = true
				if comp.Description == "" {
					t.Errorf("%s completion has no description", key)
				}
			}
		}
		if !found {
			t.Errorf("%s missing from /config:set completions", key)
		}
	}
}

// TestRetrySettingsRootLabel_ShowsStallRetry keeps the /config root summary in
// step with the new timing.
func TestRetrySettingsRootLabel_ShowsStallRetry(t *testing.T) {
	ctx, _, _ := stallTimingContext(t)
	label := retrySettingsLabel(ctx.Config)
	if label != "5 retries, 225 (warn at 200) stall retry" {
		t.Errorf("retrySettingsLabel = %q, want the stall timing in plain seconds", label)
	}
}

// guard: the retry settings sub-menu keeps a stable label set.
func TestRetrySettingsMenu_Labels(t *testing.T) {
	ctx, sr, _ := stallTimingContext(t)
	menu := newConfigMenu(*ctx)
	menu.settingRetrySettings()

	labels := map[string]string{}
	for _, item := range sr.options {
		labels[item.Value] = item.Label
	}
	for value, want := range map[string]string{
		"stall_timeout": "Auto-retry after provider silence",
		"stall_warn":    "Stall warning after provider silence",
	} {
		if labels[value] != want {
			t.Errorf("%s label = %q, want %q", value, labels[value], want)
		}
	}
	if _, ok := labels["provider_idle"]; !ok {
		t.Error("provider idle timeout entry must stay in the retry settings menu")
	}
}

// drainFlashes collects every flash currently queued on the event bus so a
// test can assert on (or discard) notifications.
func drainFlashes(t *testing.T, ctx *core.Context) []string {
	t.Helper()
	var texts []string
	for {
		select {
		case ev := <-ctx.EventBus.Chat:
			if ev.Flash != nil {
				texts = append(texts, ev.Flash.Text)
			}
		default:
			return texts
		}
	}
}

// assertStaleWarnLeadGone verifies the drop landed everywhere: memory, home
// file (override deleted, not merely healed), reload, and the live session.
// wantTimeout is the window the test shrank to.
func assertStaleWarnLeadGone(t *testing.T, ctx *core.Context, wantTimeout string) {
	t.Helper()
	if got := ctx.Config.Execution.ActivityTimeout; got != wantTimeout {
		t.Errorf("config activity_timeout = %q, want %s", got, wantTimeout)
	}
	if got := ctx.Config.Execution.ActivityWarnAfter; got != "" {
		t.Errorf("config activity_warn_after = %q, want empty (stale lead dropped)", got)
	}

	// On disk: the stale lead is DELETED, not merely healed on load.
	homePath := ctx.ConfigSaver.(*config.CascadeLoader).HomeConfigPath()
	raw, err := os.ReadFile(homePath)
	if err != nil {
		t.Fatalf("read home config: %v", err)
	}
	if strings.Contains(string(raw), "activity_warn_after") {
		t.Errorf("home config still contains the stale lead:\n%s", raw)
	}
	// Reload merges the cascade: with the home override deleted the lead falls
	// back to the shipped default. The point is the stale override is GONE, not
	// that the effective lead is unset.
	reloaded, err := ctx.ConfigSaver.(*config.CascadeLoader).Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Execution.ActivityTimeout != wantTimeout {
		t.Errorf("reloaded activity_timeout = %s, want %s", reloaded.Execution.ActivityTimeout, wantTimeout)
	}
	if reloaded.Execution.ActivityWarnAfter == "40s" {
		t.Errorf("reloaded activity_warn_after = %q, want the stale override gone", reloaded.Execution.ActivityWarnAfter)
	}

	// Live session: new window, no lead — the agent derives 2/3 of the event
	// stall.
	live := liveStreamOptions(t, ctx)
	if live.IdleTimeout.String() != wantTimeout {
		t.Errorf("running session window = %s, want %s", live.IdleTimeout, wantTimeout)
	}
	if live.ActivityWarnAfter != 0 {
		t.Errorf("running session lead = %s, want 0 (unset: the agent derives 2/3 of the event stall)", live.ActivityWarnAfter)
	}
}

// TestConfigSet_ActivityTimeoutDropsStaleWarnLead: shrinking the retry window
// so the persisted warning lead no longer fits must drop the lead — in memory
// AND on disk, in one /config set — leaving the runtime to derive 2/3 of the
// new event stall (bugs.md: goa must never keep or persist a contradictory
// stall pair). The seeded 40s lead is shorter than a 50s window but LONGER
// than its 37.5s event stall, so this also pins the split: "fits the byte
// budget" is no longer enough for the lead to survive.
func TestConfigSet_ActivityTimeoutDropsStaleWarnLead(t *testing.T) {
	ctx, _, _ := stallTimingContextWithHomeConfig(t,
		"execution:\n  activity_timeout: 60s\n  activity_warn_after: 40s\n")
	if ctx.Config.Execution.ActivityTimeout != "60s" || ctx.Config.Execution.ActivityWarnAfter != "40s" {
		t.Fatalf("precondition: loaded pair = (%s, %s), want (60s, 40s)",
			ctx.Config.Execution.ActivityTimeout, ctx.Config.Execution.ActivityWarnAfter)
	}
	drainFlashes(t, ctx)

	if err := applyConfigSet(*ctx, "execution.activity_timeout", "50s"); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}
	assertStaleWarnLeadGone(t, ctx, "50s")

	// The drop must be announced, not silent.
	announced := false
	for _, f := range drainFlashes(t, ctx) {
		if strings.Contains(f, "Stall warning lead dropped") {
			announced = true
		}
	}
	if !announced {
		t.Error("no \"Stall warning lead dropped\" flash after the shrink")
	}
}

// assertWarnSetRefused verifies a refused out-of-window lead changes nothing:
// memory, home file (which must not even appear), and the live session.
func assertWarnSetRefused(t *testing.T, ctx *core.Context) {
	t.Helper()
	if got := ctx.Config.Execution.ActivityWarnAfter; got != "3m20s" {
		t.Errorf("config activity_warn_after = %q, want 3m20s (refusal must not change it)", got)
	}
	if got := ctx.Config.Execution.ActivityTimeout; got != "5m" {
		t.Errorf("config activity_timeout = %q, want 5m", got)
	}
	homePath := ctx.ConfigSaver.(*config.CascadeLoader).HomeConfigPath()
	if _, err := os.Stat(homePath); err == nil {
		t.Error("refused /config set created a home config file")
	}
	live := liveStreamOptions(t, ctx)
	if live.ActivityWarnAfter != 200*time.Second || live.IdleTimeout != 5*time.Minute {
		t.Errorf("running session options = lead %s / window %s, want 3m20s / 5m",
			live.ActivityWarnAfter, live.IdleTimeout)
	}
	rejected := false
	for _, f := range drainFlashes(t, ctx) {
		if strings.Contains(f, "Rejected execution.activity_warn_after") {
			rejected = true
		}
	}
	if !rejected {
		t.Error("no rejection flash for the out-of-window lead")
	}
}

// TestConfigSet_ActivityWarnAboveWindowRejected: an explicitly typed warning
// lead at or beyond the stall window the agent retries on is REFUSED — nothing
// changes in memory, on disk, or in the running session — and the rejection is
// visible (flash). The shipped 5m budget means the boundary is the 3m45s event
// stall, not 5m: a 4m lead fits the byte budget but could never fire.
func TestConfigSet_ActivityWarnAboveWindowRejected(t *testing.T) {
	for _, warn := range []string{"4m", "3m45s"} { // beyond AND exactly at the event stall
		t.Run(warn, func(t *testing.T) {
			ctx, _, _ := stallTimingContext(t)
			drainFlashes(t, ctx)

			if err := applyConfigSet(*ctx, "execution.activity_warn_after", warn); err != nil {
				t.Fatalf("applyConfigSet: %v", err)
			}
			assertWarnSetRefused(t, ctx)
		})
	}
}

// TestConfigSet_PersistsCanonicalSeconds is the regression test for the
// 2026-09-26 poisoning bug: /config accepted bare seconds ("60") — the UI
// speaks plain seconds (BUG-5) — but persisted the RAW string into the home
// config, which the next start then rejected ("missing unit in duration").
// The file must always receive the canonical committed value ("60s"), the
// live session must follow, and a reload must accept the file.
func TestConfigSet_PersistsCanonicalSeconds(t *testing.T) {
	ctx, _, _ := stallTimingContext(t)

	if err := applyConfigSet(*ctx, "execution.activity_timeout", "60"); err != nil {
		t.Fatalf("applyConfigSet(timeout): %v", err)
	}
	if err := applyConfigSet(*ctx, "execution.activity_warn_after", "30"); err != nil {
		t.Fatalf("applyConfigSet(warn): %v", err)
	}

	if got := ctx.Config.Execution.ActivityTimeout; got != "60s" {
		t.Errorf("live activity_timeout = %q, want 60s", got)
	}
	if got := ctx.Config.Execution.ActivityWarnAfter; got != "30s" {
		t.Errorf("live activity_warn_after = %q, want 30s", got)
	}

	checkPersistedCanonicalStall(t, ctx)
	checkReloadedCanonicalStall(t, ctx)
}

// checkPersistedCanonicalStall asserts the RAW on-disk bytes after the two
// bare-second sets: canonical "60s"/"30s" must be present and no bare value
// may survive the write.
func checkPersistedCanonicalStall(t *testing.T, ctx *core.Context) {
	t.Helper()
	saver := ctx.ConfigSaver.(*config.CascadeLoader)
	raw, err := os.ReadFile(saver.HomeConfigPath())
	if err != nil {
		t.Fatalf("read home config: %v", err)
	}
	text := string(raw)
	for _, want := range []string{"activity_timeout: 60s", "activity_warn_after: 30s"} {
		if !strings.Contains(text, want) {
			t.Errorf("persisted file must contain %q, got:\n%s", want, text)
		}
	}
	// Anchored on the newline: "60s\n" must not satisfy a check for "60\n".
	if strings.Contains(text, "activity_timeout: 60\n") || strings.Contains(text, "activity_warn_after: 30\n") {
		t.Errorf("raw unit-less values were persisted:\n%s", text)
	}
}

// checkReloadedCanonicalStall asserts the persisted file reloads cleanly and
// the live stream options reflect the committed timings.
func checkReloadedCanonicalStall(t *testing.T, ctx *core.Context) {
	t.Helper()
	saver := ctx.ConfigSaver.(*config.CascadeLoader)
	reloaded, err := saver.Load()
	if err != nil {
		t.Fatalf("reload persisted config: %v", err)
	}
	if reloaded.Execution.ActivityTimeout != "60s" || reloaded.Execution.ActivityWarnAfter != "30s" {
		t.Errorf("reloaded = (%q, %q), want (60s, 30s)",
			reloaded.Execution.ActivityTimeout, reloaded.Execution.ActivityWarnAfter)
	}
	live := liveStreamOptions(t, ctx)
	if live.IdleTimeout != 60*time.Second || live.ActivityWarnAfter != 30*time.Second {
		t.Errorf("live options = warn %s / window %s, want 30s / 60s", live.ActivityWarnAfter, live.IdleTimeout)
	}
}
