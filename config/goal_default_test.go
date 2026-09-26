// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/tools"
)

// TestDefaultConfig_GoalToolEnabledByDefault pins the shipped default: the
// embedded config carries tools.enabled.goal: true, so the model can create
// goals with no config edit, and a cascade load of the shipped defaults yields
// cfg.Tools.Enabled.Goal == true. A home/project pin of goal: false still wins
// (the flag remains a real, honoured opt-OUT).
func TestDefaultConfig_GoalToolEnabledByDefault(t *testing.T) {
	// Isolate from the developer's real ~/.goa/config.yaml: home pins are
	// legitimate overrides, and this test asserts the SHIPPED default.
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	proj := t.TempDir()
	loader := NewCascadeLoader(proj, "", nil)

	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if !cfg.Tools.Enabled.Goal {
		t.Errorf("Tools.Enabled.Goal = false, want true (the goal tool is enabled by default)")
	}

	// A project pin of goal: false must override the shipped default.
	mustMkdir(t, filepath.Join(proj, ".goa"))
	writeFile(t, filepath.Join(proj, ".goa", "config.yaml"), "tools:\n  enabled:\n    goal: false\n")

	cfg2, err := loader.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if cfg2.Tools.Enabled.Goal {
		t.Error("a project pin of tools.enabled.goal: false must win over the shipped default")
	}

	// A HOME pin of goal: false wins too (checked on a clean project so the
	// project pin above cannot answer for it).
	mustMkdir(t, filepath.Join(home, ".goa"))
	writeFile(t, filepath.Join(home, ".goa", "config.yaml"), "tools:\n  enabled:\n    goal: false\n")

	cfg3, err := NewCascadeLoader(t.TempDir(), "", nil).Load()
	if err != nil {
		t.Fatalf("home-pin load: %v", err)
	}
	if cfg3.Tools.Enabled.Goal {
		t.Error("a home pin of tools.enabled.goal: false must win over the shipped default")
	}
}

// TestConfigurableTools_GoalDefaultMatchesEmbeddedConfig keeps /config, /docs
// and the config cascade in agreement: the `Default` field ConfigurableTools
// reports for `goal` must equal the value the embedded default config loads.
// The test lives in the config package because config imports tools
// (tools cannot import config — import cycle).
func TestConfigurableTools_GoalDefaultMatchesEmbeddedConfig(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)

	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	shipped := cfg.Tools.Enabled.Goal

	var found bool
	for _, ct := range tools.ConfigurableTools() {
		if ct.Name != "goal" {
			continue
		}
		found = true
		if ct.Default != shipped {
			t.Errorf("ConfigurableTools() goal Default = %v, want %v (embedded default config)", ct.Default, shipped)
		}
	}
	if !found {
		t.Fatal("ConfigurableTools() has no `goal` entry")
	}
	if !shipped {
		t.Error("the embedded default must ship tools.enabled.goal: true")
	}
}
