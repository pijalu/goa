// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/tui"
)

// Regression (bug: deleting the active team left teams.active pointing at a
// removed definition, hard-failing startup validation). The /config → Teams →
// remove path must clear AND persist teams.active to the project local layer
// when the deleted team is active, so a cascade reload starts cleanly.
func TestConfigMenu_RemoveActiveTeamClearsPersistedActive(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	projectDir := t.TempDir()

	cfg := teamCmdConfig()
	cfg.Teams.Active = "alpha"
	saver := config.NewCascadeLoader(projectDir, "", nil)
	// Seed both layers as a real session would: definitions at home, the
	// active selection in the project local layer.
	if err := saver.SaveHomeFieldValue([]string{"teams", "definitions"}, cfg.Teams.Definitions); err != nil {
		t.Fatalf("seed home definitions: %v", err)
	}
	if err := saver.SaveLocalFieldValue([]string{"teams", "active"}, "alpha"); err != nil {
		t.Fatalf("seed local teams.active: %v", err)
	}

	ctx, _, _, _ := newMenuTestContext(t, cfg)
	ctx.ConfigSaver = saver

	// Drive confirmRemoveTeam directly: capture its confirmation selector and
	// answer "yes".
	var callbacks []func(string, bool)
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		callbacks = append(callbacks, cb)
	}
	menu := newConfigMenu(*ctx)
	menu.confirmRemoveTeam("alpha")
	if len(callbacks) != 1 {
		t.Fatalf("callbacks = %d, want the remove confirmation selector", len(callbacks))
	}
	callbacks[0]("yes", true)

	if _, ok := cfg.Teams.Definitions["alpha"]; ok {
		t.Fatal("alpha should be removed after confirmation")
	}
	if cfg.Teams.Active != "" {
		t.Errorf("teams.active = %q in memory, want cleared", cfg.Teams.Active)
	}

	// The local layer must carry the cleared value.
	localData, err := os.ReadFile(filepath.Join(projectDir, ".goa", "config.local.yaml"))
	if err != nil {
		t.Fatalf("read local config: %v", err)
	}
	if !strings.Contains(string(localData), `active: ""`) {
		t.Errorf("local config = %q, want teams.active cleared", string(localData))
	}

	// Full restart path: reload through the cascade — must not fail
	// validation and must resolve to no active team.
	reloaded, err := config.NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("Load after active-team delete: %v", err)
	}
	if reloaded.Teams.Active != "" {
		t.Errorf("Teams.Active = %q after reload, want empty", reloaded.Teams.Active)
	}
}
