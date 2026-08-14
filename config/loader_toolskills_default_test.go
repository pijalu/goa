// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"path/filepath"
	"testing"
)

// TestCascadeToolsEnabled_HomeIsDefaultOverEmbedded verifies that the current
// global (home) config is the default for tools.enabled: a key set only in
// ~/.goa/config.yaml wins over the embedded default, while keys the home
// config does not mention keep their embedded defaults (the home layer need
// not restate every key).
func TestCascadeToolsEnabled_HomeIsDefaultOverEmbedded(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
tools:
  enabled:
    verify: false
    memento: true
    goal: true
`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Home pins override the embedded defaults (embedded: verify=true,
	// memento=false, goal unset/false).
	if cfg.Tools.Enabled.Verify {
		t.Error("Verify = true, want false (home config overrides embedded default)")
	}
	if !cfg.Tools.Enabled.Memento {
		t.Error("Memento = false, want true (home config overrides embedded default)")
	}
	if !cfg.Tools.Enabled.Goal {
		t.Error("Goal = false, want true (home config sets a key absent from embedded defaults)")
	}
	// Keys not pinned by the home layer keep their embedded defaults
	// (embedded: webfetch=true, pty_exec=true).
	if !cfg.Tools.Enabled.WebFetch {
		t.Error("WebFetch = false, want true (embedded default inherited when home is silent)")
	}
	if !cfg.Tools.Enabled.PTYExec {
		t.Error("PTYExec = false, want true (embedded default inherited when home is silent)")
	}
}

// TestCascadeToolsEnabled_ProjectOverridesHome verifies the full precedence
// chain for tools.enabled: embedded < home < project < local. Keys not pinned
// by a higher layer fall back to the current home (global) value — the home
// config is the effective default for every project.
func TestCascadeToolsEnabled_ProjectOverridesHome(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
tools:
  enabled:
    verify: false
    memento: true
    pty_exec: false
`)
	writeConfig(t, filepath.Join(projectDir, ".goa", "config.yaml"), `
tools:
  enabled:
    verify: true
`)
	writeConfig(t, filepath.Join(projectDir, ".goa", "config.local.yaml"), `
tools:
  enabled:
    pty_exec: true
`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if !cfg.Tools.Enabled.Verify {
		t.Error("Verify = false, want true (project pin overrides home default)")
	}
	if !cfg.Tools.Enabled.Memento {
		t.Error("Memento = false, want true (home default applies: project and local are silent)")
	}
	if !cfg.Tools.Enabled.PTYExec {
		t.Error("PTYExec = false, want true (local pin overrides home default)")
	}
}

// TestCascadeSkillsEnabledDisabled_HomeListsAreDefaults verifies that the
// skills enabled/disabled lists from the current global (home) config always
// apply: the cascade concatenates the lists across layers (embedded → home →
// project → local), so a skill disabled globally stays off in every project,
// and project/local layers can only add names.
func TestCascadeSkillsEnabledDisabled_HomeListsAreDefaults(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
skills:
  disabled:
    - review
    - commit-msg
`)
	writeConfig(t, filepath.Join(projectDir, ".goa", "config.yaml"), `
skills:
  disabled:
    - telegram
  enabled:
    - refactor
`)
	writeConfig(t, filepath.Join(projectDir, ".goa", "config.local.yaml"), `
skills:
  disabled:
    - review
    - document
`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// Global disabled names survive alongside project/local additions; the
	// duplicate "review" (home + local) is deduplicated.
	wantDisabled := []string{"review", "commit-msg", "telegram", "document"}
	if len(cfg.Skills.Disabled) != len(wantDisabled) {
		t.Fatalf("Skills.Disabled = %v, want %v", cfg.Skills.Disabled, wantDisabled)
	}
	for i, name := range wantDisabled {
		if cfg.Skills.Disabled[i] != name {
			t.Errorf("Skills.Disabled[%d] = %q, want %q", i, cfg.Skills.Disabled[i], name)
		}
	}

	// The project-layer allowlist is honored as-is.
	if len(cfg.Skills.Enabled) != 1 || cfg.Skills.Enabled[0] != "refactor" {
		t.Errorf("Skills.Enabled = %v, want [refactor]", cfg.Skills.Enabled)
	}
}

// TestCascadeSkillsEnabled_HomeAllowlistIsDefault verifies that a skills
// allowlist pinned only in the global (home) config is inherited by projects
// that do not set their own allowlist.
func TestCascadeSkillsEnabled_HomeAllowlistIsDefault(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
skills:
  enabled:
    - review
    - refactor
`)

	loader := NewCascadeLoader(projectDir, "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	want := []string{"review", "refactor"}
	if len(cfg.Skills.Enabled) != len(want) {
		t.Fatalf("Skills.Enabled = %v, want %v", cfg.Skills.Enabled, want)
	}
	for i, name := range want {
		if cfg.Skills.Enabled[i] != name {
			t.Errorf("Skills.Enabled[%d] = %q, want %q", i, cfg.Skills.Enabled[i], name)
		}
	}
	if len(cfg.Skills.Disabled) != 0 {
		t.Errorf("Skills.Disabled = %v, want empty", cfg.Skills.Disabled)
	}
}
