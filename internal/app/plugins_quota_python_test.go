// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/plugins"
)

// TestQuotaPythonDemo_LoadsQuotaCommand pins the Python demo contract:
// examples/plugins/quota-python (a .py entry, NOT bundled/packaged) loads
// through the shared plugins.PluginLoader and registers /quota.
func TestQuotaPythonDemo_LoadsQuotaCommand(t *testing.T) {
	loader := plugins.NewPluginLoader([]string{"../../examples/plugins"}, []string{"quota-python"})
	var commands []string
	ctx := plugins.PluginContext{
		Config: map[string]any{},
		Logger: plugins.LoggerAPI{
			Info:  func(string) {},
			Warn:  func(string) {},
			Error: func(string) {},
			Debug: func(string) {},
		},
		RegisterCommand: func(name string, aliases []string, shortHelp, longHelp string, run func([]string) (string, error)) error {
			commands = append(commands, name)
			// Run the command through the shared registry so /quota resolves
			// exactly like a JS-registered command.
			return nil
		},
	}
	bridges, err := loader.LoadAll(ctx)
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(bridges) != 1 {
		t.Fatalf("expected 1 bridge, got %d", len(bridges))
	}
	if bridges[0].Kind() != plugins.PluginKindPython {
		t.Fatalf("bridge kind = %q, want python", bridges[0].Kind())
	}
	if len(commands) != 1 || commands[0] != "quota" {
		t.Fatalf("commands = %v, want [quota]", commands)
	}
}

// TestQuotaPythonDemo_QuotaCommandOutputFilmstrip drives the demo /quota
// through the real slash-command path in a full-TUI scenario and captures it
// with the Filmstrip, proving a Python-registered command reaches the visible
// TUI exactly like its JS counterpart.
func TestQuotaPythonDemo_QuotaCommandOutputFilmstrip(t *testing.T) {
	sc := newUIScenario(t, 100, 30)
	sc.engine.RunLoops()
	// newUIScenario leaves the command registry nil; install the shared
	// registry first (mirrors production boot, where it exists before load).
	sc.app.subs.registry = core.NewCommandRegistry()

	// Load the demo plugin with the scenario's registries so /quota resolves
	// through the production router. newPluginRuntime only fills scheduler +
	// hooks when the subsystems lack them; the scenario path does, so wire
	// the production loader exactly like loadEnabledPlugins does.
	rt := newPluginRuntime(sc.app.subs)
	loader := plugins.NewPluginLoader([]string{"../../examples/plugins"}, []string{"quota-python"})
	bridges, err := loader.LoadAll(rt.contextFor(sc.app.subs))
	if err != nil {
		t.Fatalf("LoadAll: %v", err)
	}
	if len(bridges) != 1 {
		t.Fatalf("expected 1 bridge, got %d", len(bridges))
	}
	_ = bridges
	sc.app.subs.setPluginRT(rt)
	// Wire the router like the quota filmstrip scenario does: the scenario's
	// cmdRouter is nil until something builds it, and handleSlashCommand
	// parses through subs.cmdRouter (nil router = nil result/panic path).
	subs := sc.app.subs
	subs.cmdRouter = core.NewCommandRouter(subs.registry, core.NewDocEngine(subs.registry))

	cmd, ok := sc.app.subs.registry.Resolve("quota")
	if !ok {
		t.Fatal("quota command not registered by Python demo plugin")
	}
	var buf strings.Builder
	if err := cmd.Run(core.Context{OutputBuffer: &buf}, nil); err != nil {
		t.Fatalf("quota run: %v", err)
	}
	out := buf.String()
	if !strings.Contains(out, "Python quota") {
		t.Fatalf("quota output = %q, want Python quota demo text", out)
	}

	// Same output through the slash-command path, captured on film.
	film := sc.filmstrip()
	sc.engine.RenderNow()
	film.Capture("baseline-before-pyquota", sc.engine.AgentFrame(), "")
	sc.engine.ApplySync(func() { sc.app.handleSlashCommand("/quota") })
	waitFor(t, sc.engine, 3*time.Second, func() bool {
		for _, line := range sc.engine.AgentFrame().Visible {
			if strings.Contains(line, "Python quota") {
				return true
			}
		}
		return false
	}, "/quota (python demo) output never reached the viewport")
	sc.engine.RenderNow()
	snap := film.Capture("pyquota-output", sc.engine.AgentFrame(), sc.status.Text())
	assertFrameContains(t, snap, "Python quota")
}
