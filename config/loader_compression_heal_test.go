// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestLoadDropsDanglingCompressionModel is the regression test for the bug
// where deleting a model left its context_compression.per_model override
// persisted in config, hard-failing startup validation
// ("context_compression.per_model.<id>: no model with id %q is configured").
// A dangling override can also arise from manual edits across the home/project
// layers, so Load() must heal it (drop + warn) instead of refusing to start.
func TestLoadDropsDanglingCompressionModel(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	// The home config defines one model and overrides for it and for a model
	// (ghost) that no longer exists — the desync the bug produced.
	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
models:
  - id: m1
    provider: p
    model: m1
context_compression:
  enabled: true
  per_model:
    m1:
      strategy: summarize
    ghost:
      strategy: summarize
`)

	cfg, err := NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("Load must heal a dangling per_model override, got error: %v", err)
	}
	if _, ok := cfg.ContextCompression.PerModel["ghost"]; ok {
		t.Errorf("ghost override survived the heal, per_model = %v", cfg.ContextCompression.PerModel)
	}
	// The override for the surviving model must be untouched.
	if ov := cfg.ContextCompression.PerModel["m1"]; ov.Strategy != "summarize" {
		t.Errorf("m1 override = %+v, want strategy summarize preserved", ov)
	}
}

// TestLoadKeepsConfiguredCompressionModel guards the heal against overreach:
// overrides for configured models must be preserved as-is.
func TestLoadKeepsConfiguredCompressionModel(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
models:
  - id: m1
    provider: p
    model: m1
context_compression:
  enabled: true
  per_model:
    m1:
      thresholds:
        hard_percent: 90
`)

	cfg, err := NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ov, ok := cfg.ContextCompression.PerModel["m1"]
	if !ok {
		t.Fatalf("m1 override dropped, per_model = %v", cfg.ContextCompression.PerModel)
	}
	if ov.Thresholds.HardPercent != 90 {
		t.Errorf("m1 hard_percent = %d, want 90 preserved", ov.Thresholds.HardPercent)
	}
}

// TestSanitizeDanglingCompressionModelsWarns verifies the heal emits a stderr
// warning naming the dropped model so the user knows why the override is gone.
func TestSanitizeDanglingCompressionModelsWarns(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	cfg := &Config{}
	cfg.ContextCompression.PerModel = map[string]ModelCompressionOverride{
		"ghost": {Strategy: "summarize"},
	}
	cfg.sanitizeDanglingCompressionModels()

	w.Close()
	out, _ := io.ReadAll(r)
	if _, ok := cfg.ContextCompression.PerModel["ghost"]; ok {
		t.Errorf("ghost override = %v, want dropped", cfg.ContextCompression.PerModel)
	}
	if !strings.Contains(string(out), "ghost") {
		t.Errorf("warning = %q, want it to name the dropped model", string(out))
	}
}

// TestSanitizeDanglingCompressionModelsEmptyMap verifies the heal is a no-op
// on a config without per-model overrides (nil map iteration).
func TestSanitizeDanglingCompressionModelsEmptyMap(t *testing.T) {
	cfg := &Config{}
	cfg.sanitizeDanglingCompressionModels() // must not panic
	if len(cfg.ContextCompression.PerModel) != 0 {
		t.Errorf("per_model = %v, want untouched", cfg.ContextCompression.PerModel)
	}
}
