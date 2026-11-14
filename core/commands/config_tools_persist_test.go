// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/config"
)

// Regression for bugs.md "/config tool fixes are not saved / do not survive
// next load": driving the real /config menu must persist tool settings and
// the change must survive a full cascade reload.

// TestConfigMenu_ToolCallFixingToggleSurvivesReload drives /config → Tools →
// "Tool call fixing" with a real CascadeLoader, then reloads: the toggle must
// come back from disk.
func TestConfigMenu_ToolCallFixingToggleSurvivesReload(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	// Seed an explicit OFF pin: the shipped default is ON, and the toggle
	// test semantics are "flip OFF→ON, persist, reload".
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

	ctx, sr, _, _ := newMenuTestContext(t, nil)
	ctx.Config = cfg
	ctx.ConfigSaver = cl

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("tools", true)            // Tools settings:
	sr.onSel("tool_call_fixing", true) // toggles + persists + reopens menu

	reloaded, err := cl.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Execution.AutoHealToolCalls {
		t.Fatal("/config Tool call fixing toggle did not survive reload")
	}
	if !cfg.Execution.AutoHealToolCalls {
		t.Fatal("in-memory config not updated by the toggle")
	}
}

// TestConfigMenu_EnabledToolsToggleSurvivesReload drives /config → Tools →
// Enabled/disabled tools → todo_list ON with a real CascadeLoader, then
// reloads: the enablement must come back from disk.
func TestConfigMenu_EnabledToolsToggleSurvivesReload(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	cl := config.NewCascadeLoader(proj, "", nil)
	cfg, err := cl.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Tools.Enabled.GetEnabled("verify") {
		t.Fatal("precondition: verify should default disabled in this config")
	}

	ctx, sr, _, _ := newMenuTestContext(t, nil)
	ctx.Config = cfg
	ctx.ConfigSaver = cl

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("tools", true)         // Tools settings:
	sr.onSel("enabled_tools", true) // Toggle optional tools:
	sr.onSel("verify", true)        // toggle ON

	reloaded, err := cl.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Tools.Enabled.GetEnabled("verify") {
		t.Fatal("/config enabled-tools toggle did not survive reload")
	}
}
