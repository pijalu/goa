// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestSaveLocalFieldValueWritesLocalLayerOnly verifies that SaveLocalFieldValue
// persists to the project LOCAL layer (.goa/config.local.yaml — gitignored,
// per-developer) and leaves the home and project config files untouched.
// Regression test for the bug where teams.active was written to the home
// config and leaked across all projects.
func TestSaveLocalFieldValueWritesLocalLayerOnly(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), "active_provider: home-provider\n")
	writeConfig(t, filepath.Join(projectDir, ".goa", "config.yaml"), "active_provider: project-provider\n")

	loader := NewCascadeLoader(projectDir, "", nil)
	if err := loader.SaveLocalFieldValue([]string{"teams", "active"}, "alpha"); err != nil {
		t.Fatalf("SaveLocalFieldValue: %v", err)
	}

	// Local layer carries the value.
	data, err := os.ReadFile(filepath.Join(projectDir, ".goa", "config.local.yaml"))
	if err != nil {
		t.Fatalf("read local config: %v", err)
	}
	if !strings.Contains(string(data), "active: alpha") {
		t.Errorf("local config = %q, want teams.active: alpha", string(data))
	}

	// Home config untouched.
	homeData, err := os.ReadFile(filepath.Join(homeDir, ".goa", "config.yaml"))
	if err != nil {
		t.Fatalf("read home config: %v", err)
	}
	if strings.Contains(string(homeData), "teams") {
		t.Errorf("home config must not carry teams.active, got %q", string(homeData))
	}

	// Project (committed) config untouched.
	projectData, err := os.ReadFile(filepath.Join(projectDir, ".goa", "config.yaml"))
	if err != nil {
		t.Fatalf("read project config: %v", err)
	}
	if strings.Contains(string(projectData), "teams") {
		t.Errorf("project config must not carry teams.active, got %q", string(projectData))
	}
}

// TestSaveLocalFieldValueCreatesFile verifies the local file is created when
// missing (minimal document, never a dump of the merged config).
func TestSaveLocalFieldValueCreatesFile(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	loader := NewCascadeLoader(projectDir, "", nil)
	if err := loader.SaveLocalFieldValue([]string{"teams", "active"}, "beta"); err != nil {
		t.Fatalf("SaveLocalFieldValue: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(projectDir, ".goa", "config.local.yaml"))
	if err != nil {
		t.Fatalf("read local config: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "teams:") || !strings.Contains(got, "active: beta") {
		t.Errorf("local config = %q, want teams.active: beta only", got)
	}
}

// TestSaveLocalFieldValuePreservesOtherLocalSettings verifies a second write
// keeps earlier local settings (field-scoped read-modify-write).
func TestSaveLocalFieldValuePreservesOtherLocalSettings(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(projectDir, ".goa", "config.local.yaml"), "theme: dark\n")

	loader := NewCascadeLoader(projectDir, "", nil)
	if err := loader.SaveLocalFieldValue([]string{"teams", "active"}, "alpha"); err != nil {
		t.Fatalf("SaveLocalFieldValue: %v", err)
	}

	data, err := os.ReadFile(filepath.Join(projectDir, ".goa", "config.local.yaml"))
	if err != nil {
		t.Fatalf("read local config: %v", err)
	}
	got := string(data)
	if !strings.Contains(got, "theme: dark") {
		t.Errorf("local config lost prior setting: %q", got)
	}
	if !strings.Contains(got, "active: alpha") {
		t.Errorf("local config missing teams.active: %q", got)
	}
}

// TestCascadeLocalTeamsActiveResolvesOnStartup verifies a teams.active value
// persisted to the local layer wins over home and project layers when the
// configuration is loaded again (restart path).
func TestCascadeLocalTeamsActiveResolvesOnStartup(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	// The team definition lives in the home config (user level); the
	// validation on Load requires teams.active to reference a defined team.
	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
teams:
  definitions:
    alpha:
      main: {model: m1}
      review: "off"
`)

	loader := NewCascadeLoader(projectDir, "", nil)
	if err := loader.SaveLocalFieldValue([]string{"teams", "active"}, "alpha"); err != nil {
		t.Fatalf("SaveLocalFieldValue: %v", err)
	}

	reloaded, err := NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.Teams.Active != "alpha" {
		t.Errorf("Teams.Active = %q after reload, want %q from the local layer", reloaded.Teams.Active, "alpha")
	}
}
