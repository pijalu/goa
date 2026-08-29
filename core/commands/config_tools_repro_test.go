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

// Regression: /config → Tools → "Tool call fixing" toggled ON must survive a
// restart even when the project config (.goa/config.yaml) already pins
// execution.auto_heal_tool_calls: false. The toggle persists via
// SaveProjectField (project layer), which replaces the stale pin instead of
// being shadowed by it (bugs-20260823-config-tool-settings).
func TestConfigMenu_ToolCallFixingToggleOverwritesProjectPin(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	// Seed the project config with the stale pin, exactly as found on disk.
	goaDir := filepath.Join(proj, ".goa")
	if err := os.MkdirAll(goaDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	seed := "execution:\n  auto_heal_tool_calls: false\n"
	if err := os.WriteFile(filepath.Join(goaDir, "config.yaml"), []byte(seed), 0o644); err != nil {
		t.Fatalf("seed project config: %v", err)
	}

	cl := config.NewCascadeLoader(proj, "", nil)
	cfg, err := cl.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Execution.AutoHealToolCalls {
		t.Fatal("precondition: project pin should make auto_heal_tool_calls false")
	}

	ctx, sr, _, _ := newMenuTestContext(t, nil)
	ctx.Config = cfg
	ctx.ConfigSaver = cl

	menu := newConfigMenu(*ctx)
	_ = menu.showRoot()
	sr.onSel("tools", true)            // Tools settings:
	sr.onSel("tool_call_fixing", true) // toggle ON

	reloaded, err := cl.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Execution.AutoHealToolCalls {
		t.Fatal("toggle reported success but reverted to false after reload (project pin overrode the write)")
	}
}
