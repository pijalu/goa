// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	internal "github.com/pijalu/goa/internal"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/ansi"
)

// End-to-end regression for bugs.md "/config tool-call fixing is not saved /
// not applied to ongoing sessions": driving the real /config → Tools →
// "Tool call fixing" toggle must BOTH persist the value through a real
// CascadeLoader AND push it into an already-running session without restart.
func TestConfigMenu_ToolCallFixingSavedAndAppliedLive(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Seed an explicit OFF pin: the shipped default is ON, and the toggle
	// test semantics are "flip OFF→ON, persist, apply live".
	goaDir := filepath.Join(proj, ".goa")
	if err := os.MkdirAll(goaDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(goaDir, "config.yaml"),
		[]byte("execution:\n  auto_heal_tool_calls: false\n"), 0o644); err != nil {
		t.Fatalf("seed project config: %v", err)
	}
	cl := config.NewCascadeLoader(proj, "", nil)
	cfg, err := cl.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Execution.AutoHealToolCalls {
		t.Fatal("precondition: auto_heal_tool_calls should be pinned false")
	}

	ctx, sr, _, events := newMenuTestContext(t, cfg)
	ctx.Config = cfg
	ctx.ConfigSaver = cl
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
		agenticprovider.StreamOptions{}, "sys", nil, cfg,
	); err != nil {
		t.Fatalf("StartSession: %v", err)
	}
	if ctx.AgentManager.CurrentAgent().AutoHealEnabled() {
		t.Fatal("precondition: live session should start with auto-heal off")
	}

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("tools", true)            // Tools settings:
	sr.onSel("tool_call_fixing", true) // toggle ON

	// Half 1: saved in config (survives a full cascade reload).
	reloaded, err := cl.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Execution.AutoHealToolCalls {
		t.Fatal("/config tool-call fixing toggle did not survive reload")
	}

	// Half 2: directly used by the ongoing session.
	if !cfg.Execution.AutoHealToolCalls {
		t.Fatal("in-memory config not updated by the toggle")
	}
	if !ctx.AgentManager.CurrentAgent().AutoHealEnabled() {
		t.Fatal("running session did not pick up the tool-call fixing change")
	}
}

// TestConfigSet_StripANSIUpdatesRuntime verifies /config:set tools.strip_ansi
// persists the choice and immediately flips the process-wide ANSI gate used
// by untrusted tool-output producers.
func TestConfigSet_StripANSIUpdatesRuntime(t *testing.T) {
	prev := ansi.StripANSIEnabled()
	t.Cleanup(func() { ansi.SetStripANSI(prev) })
	cfg := &config.Config{}
	ctx, _, _, _ := newMenuTestContext(t, cfg)
	ansi.SetStripANSI(true)

	if err := applyConfigSet(*ctx, "tools.strip_ansi", "false"); err != nil {
		t.Fatalf("applyConfigSet: %v", err)
	}
	if cfg.Tools.StripANSIEnabled() {
		t.Error("in-memory config still has strip_ansi enabled")
	}
	if ansi.StripANSIEnabled() {
		t.Error("runtime ANSI gate still enabled after /config:set false")
	}

	reloaded, err := ctx.ConfigSaver.(*config.CascadeLoader).Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if reloaded.Tools.StripANSIEnabled() {
		t.Error("/config:set tools.strip_ansi=false did not survive reload")
	}
}
