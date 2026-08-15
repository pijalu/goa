// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRunCodeDefaultsLoaded pins the embedded defaults for the run_code
// code-mode tool (gap TL7): enabled by default, 60s timeout, and the worker
// jailed by default.
func TestRunCodeDefaultsLoaded(t *testing.T) {
	cfg, err := NewCascadeLoader(t.TempDir(), "", nil).Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if !cfg.Tools.Enabled.RunCode {
		t.Error("run_code should be enabled by default (opt-out like python)")
	}
	if cfg.Tools.RunCode.TimeoutSeconds != 60 {
		t.Errorf("run_code timeout_seconds = %d, want 60", cfg.Tools.RunCode.TimeoutSeconds)
	}
	if cfg.Tools.RunCode.Jail == nil || !*cfg.Tools.RunCode.Jail {
		t.Error("run_code jail should default to true (jailed worker)")
	}
}

// TestRunCodeDefaultsNotLostOnProjectMerge verifies a project config that only
// touches other tools does not zero the run_code defaults, and that a project
// config that sets a run_code field overrides it.
func TestRunCodeDefaultsNotLostOnProjectMerge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	projectDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(filepath.Join(projectDir, ".goa"), 0755); err != nil {
		t.Fatal(err)
	}
	projectYAML := `
tools:
  run_code:
    timeout_seconds: 30
  bash:
    blocked_commands: []
`
	if err := os.WriteFile(filepath.Join(projectDir, ".goa", "config.yaml"), []byte(projectYAML), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Tools.RunCode.TimeoutSeconds != 30 {
		t.Errorf("run_code timeout_seconds = %d, want 30 (project override)", cfg.Tools.RunCode.TimeoutSeconds)
	}
	if !cfg.Tools.Enabled.RunCode {
		t.Error("run_code should stay enabled after a project merge")
	}
}

// TestRunCodeDisabledSurvivesMerge verifies tools.enabled.run_code: false in a
// project layer disables the tool without touching other fields.
func TestRunCodeDisabledSurvivesMerge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	projectDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(filepath.Join(projectDir, ".goa"), 0755); err != nil {
		t.Fatal(err)
	}
	projectYAML := `
tools:
  enabled:
    run_code: false
`
	if err := os.WriteFile(filepath.Join(projectDir, ".goa", "config.yaml"), []byte(projectYAML), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Tools.Enabled.RunCode {
		t.Error("run_code should be disabled by the project layer")
	}
	if cfg.Tools.Python.TimeoutSeconds == 0 {
		t.Error("disabling run_code must not zero unrelated tool defaults (python timeout)")
	}
}

// TestRunCodeJailExplicitFalseOverrides verifies a project layer can opt out
// of the default jailed worker by setting run_code.jail: false.
func TestRunCodeJailExplicitFalseOverrides(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	projectDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(filepath.Join(projectDir, ".goa"), 0755); err != nil {
		t.Fatal(err)
	}
	projectYAML := `
tools:
  run_code:
    jail: false
`
	if err := os.WriteFile(filepath.Join(projectDir, ".goa", "config.yaml"), []byte(projectYAML), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Tools.RunCode.Jail == nil || *cfg.Tools.RunCode.Jail {
		t.Errorf("run_code jail = %v, want explicitly false after project merge", cfg.Tools.RunCode.Jail)
	}
}

// TestRunCodeJailDefaultSurvivesUnrelatedMerge verifies a project config that
// does not touch run_code keeps the default jailed worker.
func TestRunCodeJailDefaultSurvivesUnrelatedMerge(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("HOME", dir)
	t.Setenv("USERPROFILE", dir)
	projectDir := filepath.Join(dir, "project")
	if err := os.MkdirAll(filepath.Join(projectDir, ".goa"), 0755); err != nil {
		t.Fatal(err)
	}
	projectYAML := `
tools:
  bash:
    blocked_commands: []
`
	if err := os.WriteFile(filepath.Join(projectDir, ".goa", "config.yaml"), []byte(projectYAML), 0644); err != nil {
		t.Fatal(err)
	}

	cfg, err := NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("Load error: %v", err)
	}
	if cfg.Tools.RunCode.Jail == nil || !*cfg.Tools.RunCode.Jail {
		t.Error("run_code jail default true must survive an unrelated project merge")
	}
}
