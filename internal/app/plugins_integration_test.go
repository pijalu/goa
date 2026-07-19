// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/trust"
	"github.com/pijalu/goa/plugins"
	"github.com/pijalu/goa/plugins/bundled"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tui"
)

// newPluginTestSubsystems builds the minimal subsystems needed to exercise
// loadEnabledPlugins: a plugin manager over a temp root, a command registry,
// a tool registry, and a config with one provider.
func newPluginTestSubsystems(t *testing.T) *subsystems {
	t.Helper()
	root := t.TempDir()
	cfgDir := filepath.Join(root, ".goa")
	trustMgr := trust.NewManager(filepath.Join(cfgDir, "trust.json"))
	pluginMgr, err := plugins.NewManager(filepath.Join(cfgDir, "plugins"), trustMgr)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.Config{ConfigDir: cfgDir}
	cfg.Providers = []config.ProviderConfig{
		{ID: "anthropic", Provider: "anthropic", APIKey: "sk-ant"},
	}
	return &subsystems{
		cfg:          cfg,
		projectDir:   root,
		pluginMgr:    pluginMgr,
		registry:     core.NewCommandRegistry(),
		toolRegistry: tools.NewToolRegistry(),
	}
}

// TestLoadEnabledPlugins_BundledRegistersQuota drives the real load path:
// materialize the embedded provider-quota plugin, load it, and confirm the
// /quota command + segment + hotkey register.
func TestLoadEnabledPlugins_BundledRegistersQuota(t *testing.T) {
	s := newPluginTestSubsystems(t)
	loadEnabledPlugins(s)

	if s.pluginRT == nil {
		t.Fatal("pluginRT not set after load")
	}
	// Command registered into the shared registry.
	if _, ok := s.registry.Resolve("quota"); !ok {
		t.Fatal("/quota command not registered in shared registry")
	}
	// Segment registered on the UI bridge.
	if len(s.pluginRT.ui.Segments()) == 0 {
		t.Fatal("quota segment not registered")
	}
	// Hotkey registered.
	if len(s.pluginRT.hotkeys.Registered()) == 0 {
		t.Fatal("quota hotkey not registered")
	}
}

// TestLoadEnabledPlugins_BundledDisabled confirms the config opt-out.
func TestLoadEnabledPlugins_BundledDisabled(t *testing.T) {
	s := newPluginTestSubsystems(t)
	s.cfg.Plugins.Bundled = map[string]bool{bundled.ProviderQuotaID: false}
	loadEnabledPlugins(s)
	// Nothing enabled → pluginRT stays nil.
	if s.pluginRT != nil {
		t.Fatal("pluginRT set despite bundled plugin disabled")
	}
}

// TestLoadEnabledPlugins_NoPluginsFlag confirms the --no-plugins CLI flag
// skips plugin loading entirely, even when plugins are installed and enabled.
func TestLoadEnabledPlugins_NoPluginsFlag(t *testing.T) {
	s := newPluginTestSubsystems(t)
	s.noPlugins = true
	loadEnabledPlugins(s)
	if s.pluginRT != nil {
		t.Fatal("pluginRT set despite --no-plugins")
	}
	if _, ok := s.registry.Resolve("quota"); ok {
		t.Fatal("/quota registered despite --no-plugins")
	}
}

// TestMaterializeBundledPlugins_Idempotent verifies repeated loads don't
// error and reuse the versioned dir.
func TestMaterializeBundledPlugins_Idempotent(t *testing.T) {
	s := newPluginTestSubsystems(t)
	d1 := materializeBundledPlugins(s)
	d2 := materializeBundledPlugins(s)
	if d1 == "" || d1 != d2 {
		t.Fatalf("materialize not idempotent: %q vs %q", d1, d2)
	}
	if !s.pluginMgr.IsEnabled(bundled.ProviderQuotaID) {
		t.Fatal("provider-quota not enabled after materialize")
	}
}

// TestStartAsyncPluginLoad_LoadsInBackground covers the startup-speed feature:
// plugins load on a background goroutine (so the first frame is not blocked),
// pluginsLoaded closes on completion, and the plugin runtime + UI activate.
func TestStartAsyncPluginLoad_LoadsInBackground(t *testing.T) {
	s := newPluginTestSubsystems(t)
	a := New(s)
	engine := tui.NewTUI(&testTerminal{w: 80, h: 24})

	start := time.Now()
	a.startAsyncPluginLoad(engine)
	// startAsyncPluginLoad must return immediately (not block on the ~0.5s load).
	if d := time.Since(start); d > 200*time.Millisecond {
		t.Fatalf("startAsyncPluginLoad blocked %v; it must return immediately", d)
	}
	if a.pluginsLoaded == nil {
		t.Fatal("pluginsLoaded channel not set")
	}
	// Wait for the background load to complete, then the runtime must be set.
	select {
	case <-a.pluginsLoaded:
	case <-time.After(10 * time.Second):
		t.Fatal("async plugin load did not complete in time")
	}
	if s.getPluginRT() == nil {
		t.Fatal("pluginRT not set after async load")
	}
	if _, ok := s.registry.Resolve("quota"); !ok {
		t.Fatal("/quota not registered after async load")
	}
}

// TestStartAsyncPluginLoad_NoPluginsFlagSkips confirms the --no-plugins flag
// short-circuits the async loader (no goroutine, no channel).
func TestStartAsyncPluginLoad_NoPluginsFlagSkips(t *testing.T) {
	s := newPluginTestSubsystems(t)
	s.noPlugins = true
	a := New(s)
	a.startAsyncPluginLoad(tui.NewTUI(&testTerminal{w: 80, h: 24}))
	if a.pluginsLoaded != nil {
		t.Fatal("pluginsLoaded should be nil when plugins are disabled")
	}
	if s.getPluginRT() != nil {
		t.Fatal("pluginRT set despite --no-plugins")
	}
}

// TestPluginCommandExecutesThroughRouter runs the registered /quota command
// via the command router to confirm end-to-end output flows.
func TestPluginCommandExecutesThroughRouter(t *testing.T) {
	s := newPluginTestSubsystems(t)
	loadEnabledPlugins(s)

	cmd, ok := s.registry.Resolve("quota")
	if !ok {
		t.Fatal("quota not resolved")
	}
	ctx := core.Context{Config: s.cfg, ProjectDir: s.projectDir}
	var buf strings.Builder
	ctx.OutputBuffer = &buf
	if err := cmd.Run(ctx, []string{}); err != nil {
		t.Fatalf("quota run: %v", err)
	}
	out := buf.String()
	if out == "" {
		t.Fatal("quota produced no output")
	}
	if !strings.Contains(out, "Session Usage") || !strings.Contains(out, "Provider Quotas") {
		t.Fatalf("quota output incomplete:\n%s", out)
	}
}
