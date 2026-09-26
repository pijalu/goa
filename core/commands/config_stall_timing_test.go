// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
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
// same shape the app uses. It returns the context and the live agent's stream
// options accessor.
func stallTimingContext(t *testing.T) (*core.Context, *selectRecorder, *inputRecorder) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	loader := config.NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Execution.ActivityTimeout != "45s" || cfg.Execution.ActivityWarnAfter != "30s" {
		t.Fatalf("precondition: shipped stall timing = (%q, %q), want (45s, 30s)",
			cfg.Execution.ActivityTimeout, cfg.Execution.ActivityWarnAfter)
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

	// Shipped defaults already reach the live session.
	live := liveStreamOptions(t, ctx)
	if live.ActivityWarnAfter != 30*time.Second || live.IdleTimeout != 45*time.Second {
		t.Fatalf("live options = warn %s / window %s, want 30s / 45s", live.ActivityWarnAfter, live.IdleTimeout)
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
// user can retune 45s/30s live.
func TestConfigSet_ActivityTimeoutPushesLiveOptions(t *testing.T) {
	ctx, _, _ := stallTimingContext(t)

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
	if live.ActivityWarnAfter != 30*time.Second {
		t.Errorf("warning lead = %s, want the configured 30s (window change must not disturb it)", live.ActivityWarnAfter)
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
	if windowDesc != "45s (warn at 30s)" {
		t.Errorf("retry-window description = %q, want \"45s (warn at 30s)\"", windowDesc)
	}
	warnDesc, ok := descriptions["stall_warn"]
	if !ok {
		t.Fatalf("retry settings menu is missing the stall-warning entry: %+v", sr.options)
	}
	if warnDesc != "30s" {
		t.Errorf("stall-warning description = %q, want \"30s\"", warnDesc)
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
	if refreshed != "20s" {
		t.Errorf("menu did not re-render the new lead, got %q", refreshed)
	}
}

// TestRetrySettingsMenu_DerivesWarningWhenUnset: with no explicit lead the menu
// must show the value the agent would derive (two thirds of the window) rather
// than an empty field.
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
	if windowDesc != "45s (warn at 30s)" {
		t.Errorf("derived window description = %q, want \"45s (warn at 30s)\"", windowDesc)
	}
	if warnDesc != "30s (derived: 2/3 of 45s)" {
		t.Errorf("derived warning description = %q, want \"30s (derived: 2/3 of 45s)\"", warnDesc)
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
	if label != "5 retries, 45s (warn at 30s) stall retry" {
		t.Errorf("retrySettingsLabel = %q, want the stall timing in the summary", label)
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
