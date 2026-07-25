// SPDX-License-Identifier: GPL-3.0-or-later

package config

import (
	"path/filepath"
	"testing"
)

// TestToolToggle_PersistsToProjectOverStalePin is the regression test for the
// goal enable/disable toggle reverting on restart. The frigolite project config
// carried a stale tools.enabled.goal:false pin; the /config toggle wrote the
// new value to the HOME config, which the cascade (project > home) then
// overrode — so the toggle never took effect. Toggles must persist to the
// PROJECT config (the highest-precedence writable layer).
func TestToolToggle_PersistsToProjectOverStalePin(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	mustMkdir(t, filepath.Join(home, ".goa"))
	mustMkdir(t, filepath.Join(proj, ".goa"))

	// Stale pin: goal disabled in the project config.
	writeFile(t, filepath.Join(proj, ".goa", "config.yaml"), "tools:\n  enabled:\n    goal: false\n")

	cl := NewCascadeLoader(proj, "", map[string]string{})
	cl.homeDir = home

	cfg, err := cl.Load()
	if err != nil {
		t.Fatalf("load: %v", err)
	}
	if cfg.Tools.Enabled.Goal {
		t.Fatalf("precondition: goal should load false from project pin, got true")
	}

	// The user toggles goal ON. The command layer persists via SaveProjectField.
	if err := cl.SaveProjectField([]string{"tools", "enabled", "goal"}, true); err != nil {
		t.Fatalf("SaveProjectField: %v", err)
	}

	// Restart: reload the cascade and confirm the toggle took effect.
	reloaded, err := cl.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Tools.Enabled.Goal {
		t.Errorf("goal toggle did not persist: expected goal ON after SaveProjectField(true), got OFF")
	}

	// And toggling back OFF must also persist.
	if err := cl.SaveProjectField([]string{"tools", "enabled", "goal"}, false); err != nil {
		t.Fatalf("SaveProjectField(false): %v", err)
	}
	reloaded2, err := cl.Load()
	if err != nil {
		t.Fatalf("reload2: %v", err)
	}
	if reloaded2.Tools.Enabled.Goal {
		t.Errorf("goal toggle-off did not persist: expected goal OFF, got ON")
	}
}

// TestSaveProjectField_CreatesFileWhenMissing ensures SaveProjectField writes
// the field even for a project without an existing .goa/config.yaml (matching
// SaveHomeField), so tool toggles persist for every project.
func TestSaveProjectField_CreatesFileWhenMissing(t *testing.T) {
	home := t.TempDir()
	proj := t.TempDir()
	mustMkdir(t, filepath.Join(home, ".goa"))
	// NOTE: no project .goa/config.yaml created.

	cl := NewCascadeLoader(proj, "", map[string]string{})
	cl.homeDir = home

	if err := cl.SaveProjectField([]string{"tools", "enabled", "goal"}, true); err != nil {
		t.Fatalf("SaveProjectField: %v", err)
	}

	reloaded, err := cl.Load()
	if err != nil {
		t.Fatalf("reload: %v", err)
	}
	if !reloaded.Tools.Enabled.Goal {
		t.Errorf("SaveProjectField did not persist goal:true for a project without an existing config file")
	}
}
