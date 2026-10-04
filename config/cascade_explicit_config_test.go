// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"path/filepath"
	"testing"
)

// TestCascadeExplicitConfigFile_ReplacesProjectLayersOnly pins the semantics
// docs/CLI.md documents for `--config`: the named file REPLACES the two project
// layers (.goa/config.yaml, .goa/config.local.yaml) — it does not replace the
// cascade. The home layer, the embedded defaults, GOA_* variables and the CLI
// flags still apply. Documenting it as "replaces the cascade" was wrong; this
// test is what keeps the documentation honest.
func TestCascadeExplicitConfigFile_ReplacesProjectLayersOnly(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()

	t.Setenv("HOME", homeDir)

	// A home-only setting that must survive the explicit file.
	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
active_model: home-model
tui:
  theme: light
`)

	// Project-only settings that must NOT be read when --config is given.
	writeConfig(t, filepath.Join(projectDir, ".goa", "config.yaml"), `active_provider: project-provider`)
	writeConfig(t, filepath.Join(projectDir, ".goa", "config.local.yaml"), `active_provider: local-provider`)

	explicitPath := filepath.Join(homeDir, "custom.yaml")
	writeConfig(t, explicitPath, `active_model: explicit-model`)

	loader := NewCascadeLoader(projectDir, explicitPath, nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if cfg.ActiveModel != "explicit-model" {
		t.Errorf("ActiveModel = %q, want %q (the explicit file must win over home)", cfg.ActiveModel, "explicit-model")
	}
	if cfg.TUI.Theme != "light" {
		t.Errorf("TUI.Theme = %q, want %q (the home layer must still be merged)", cfg.TUI.Theme, "light")
	}
	if cfg.ActiveProvider == "project-provider" || cfg.ActiveProvider == "local-provider" {
		t.Errorf("ActiveProvider = %q — --config replaces the project layers, it does not merge with them",
			cfg.ActiveProvider)
	}
}
