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

// TestLoadClearsDanglingTeamsActive is the regression test for the bug where
// deleting the active team left teams.active pointing at a removed definition,
// hard-failing startup validation ("teams.active: team %q not defined in
// teams.definitions"). A dangling selection can also arise from manual edits
// across the home/local layers, so Load() must heal it (equivalent to
// /team:off) instead of refusing to start.
func TestLoadClearsDanglingTeamsActive(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	// The home config defines one team (beta); the local layer selects a
	// team (ghost) that no longer exists — the desync the bug produced.
	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
teams:
  definitions:
    beta:
      main: {model: m1}
      review: "off"
`)
	writeConfig(t, filepath.Join(projectDir, ".goa", "config.local.yaml"), `
teams:
  active: ghost
`)

	cfg, err := NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("Load must heal a dangling teams.active, got error: %v", err)
	}
	if cfg.Teams.Active != "" {
		t.Errorf("Teams.Active = %q after load, want cleared", cfg.Teams.Active)
	}
	// The surviving definition must be untouched.
	if _, ok := cfg.Teams.Definitions["beta"]; !ok {
		t.Errorf("beta definition must survive the heal, definitions = %v", cfg.Teams.Definitions)
	}
}

// TestLoadKeepsDefinedTeamsActive guards the heal against overreach: a
// teams.active naming a defined team must be preserved as-is.
func TestLoadKeepsDefinedTeamsActive(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
teams:
  definitions:
    beta:
      main: {model: m1}
      review: "off"
`)
	writeConfig(t, filepath.Join(projectDir, ".goa", "config.local.yaml"), `
teams:
  active: beta
`)

	cfg, err := NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Teams.Active != "beta" {
		t.Errorf("Teams.Active = %q after load, want beta preserved", cfg.Teams.Active)
	}
}

// TestSanitizeDanglingActiveTeamWarns verifies the heal emits a stderr
// warning naming the dropped team so the user knows why the selection is
// gone.
func TestSanitizeDanglingActiveTeamWarns(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	cfg := &Config{}
	cfg.Teams.Active = "ghost"
	cfg.sanitizeDanglingActiveTeam()

	w.Close()
	out, _ := io.ReadAll(r)
	if cfg.Teams.Active != "" {
		t.Errorf("Teams.Active = %q, want cleared", cfg.Teams.Active)
	}
	if !strings.Contains(string(out), "ghost") {
		t.Errorf("warning = %q, want it to name the dropped team", string(out))
	}
}
