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
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/team"
	"github.com/pijalu/goa/tui"
)

// teamCmdConfig builds a config with two shorthand teams.

func TestTeamCommand_NoManager(t *testing.T) {
	ctx := core.Context{Config: teamCmdConfig()}
	var out strings.Builder
	ctx.OutputBuffer = &out
	if err := (&TeamCommand{}).Run(ctx, nil); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "unavailable") {
		t.Errorf("out = %q", out.String())
	}
}

func TestTeamCommand_ActivateDirect(t *testing.T) {
	cfg := teamCmdConfig()
	ctx := teamCmdContext(t, cfg)
	var out strings.Builder
	ctx.OutputBuffer = &out
	if err := (&TeamCommand{}).Run(ctx, []string{"alpha"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if !strings.Contains(out.String(), "Team active: alpha") {
		t.Errorf("out = %q", out.String())
	}
	if cfg.Teams.Active != "alpha" {
		t.Errorf("teams.active = %q, want alpha persisted", cfg.Teams.Active)
	}
}

// Regression (bug: activating a team persisted teams.active to the HOME
// config, leaking the selection across all projects): activation must write
// to the project LOCAL layer (.goa/config.local.yaml — gitignored,
// per-developer) and leave both the home config and the committed project
// config untouched.

// Regression (bug: activating a team persisted teams.active to the HOME
// config, leaking the selection across all projects): activation must write
// to the project LOCAL layer (.goa/config.local.yaml — gitignored,
// per-developer) and leave both the home config and the committed project
// config untouched.
func TestTeamCommand_ActivatePersistsToLocalLayer(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	projectDir := t.TempDir()

	cfg := teamCmdConfig()
	sess := &stubSession{providerID: "p0", modelID: "m0"}
	m := team.NewManager(cfg, sess, nil, nil, nil, nil)
	saver := config.NewCascadeLoader(projectDir, "", nil)
	// Definitions persist to the home config at creation time; the reload
	// below validates teams.active against the on-disk definitions.
	if err := saver.SaveHomeFieldValue([]string{"teams", "definitions"}, cfg.Teams.Definitions); err != nil {
		t.Fatalf("seed home definitions: %v", err)
	}
	ctx := core.Context{
		Config:      cfg,
		TeamManager: m,
		ConfigSaver: saver,
	}
	var out strings.Builder
	ctx.OutputBuffer = &out

	if err := (&TeamCommand{}).Run(ctx, []string{"alpha"}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The local layer file carries the value.
	localPath := filepath.Join(projectDir, ".goa", "config.local.yaml")
	localData, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("teams.active must be written to %s: %v", localPath, err)
	}
	if !strings.Contains(string(localData), "active: alpha") {
		t.Errorf("local config = %q, want teams.active: alpha", string(localData))
	}

	// The home config carries the definitions but must NOT carry teams.active.
	homePath := filepath.Join(homeDir, ".goa", "config.yaml")
	if homeData, err := os.ReadFile(homePath); err == nil && strings.Contains(string(homeData), "active") {
		t.Errorf("home config must not carry teams.active, got %q", string(homeData))
	}

	// The committed project config must NOT carry the value either.
	projectPath := filepath.Join(projectDir, ".goa", "config.yaml")
	if projectData, err := os.ReadFile(projectPath); err == nil && strings.Contains(string(projectData), "teams") {
		t.Errorf("project config must not carry teams.active, got %q", string(projectData))
	}

	// A reload through the startup cascade resolves teams.active from the
	// local layer.
	reloaded, err := config.NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if reloaded.Teams.Active != "alpha" {
		t.Errorf("Teams.Active = %q after reload, want %q from the local layer", reloaded.Teams.Active, "alpha")
	}
}

// /team:off clears teams.active in the local layer, not the home config.

// /team:off clears teams.active in the local layer, not the home config.
func TestTeamCommand_OffPersistsToLocalLayer(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	projectDir := t.TempDir()

	cfg := teamCmdConfig()
	sess := &stubSession{providerID: "p0", modelID: "m0"}
	m := team.NewManager(cfg, sess, nil, nil, nil, nil)
	ctx := core.Context{
		Config:      cfg,
		TeamManager: m,
		ConfigSaver: config.NewCascadeLoader(projectDir, "", nil),
	}
	var out strings.Builder
	ctx.OutputBuffer = &out

	_ = (&TeamCommand{}).Run(ctx, []string{"alpha"})
	_ = (&TeamCommand{}).Run(ctx, []string{"off"})

	localPath := filepath.Join(projectDir, ".goa", "config.local.yaml")
	localData, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("teams.active must be written to %s: %v", localPath, err)
	}
	if !strings.Contains(string(localData), "active: \"\"") {
		t.Errorf("local config = %q, want teams.active cleared", string(localData))
	}
	homePath := filepath.Join(homeDir, ".goa", "config.yaml")
	if homeData, err := os.ReadFile(homePath); err == nil && strings.Contains(string(homeData), "teams") {
		t.Errorf("home config must not carry teams.active, got %q", string(homeData))
	}
}

func TestTeamCommand_ActivateUnknown(t *testing.T) {
	cfg := teamCmdConfig()
	ctx := teamCmdContext(t, cfg)
	var out strings.Builder
	ctx.OutputBuffer = &out
	if err := (&TeamCommand{}).Run(ctx, []string{"ghost"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	got := out.String()
	if !strings.Contains(got, "ghost") || !strings.Contains(got, "alpha") {
		t.Errorf("out = %q, want missing team + defined list", got)
	}
}

func TestTeamCommand_StatusAndList(t *testing.T) {
	cfg := teamCmdConfig()
	ctx := teamCmdContext(t, cfg)
	var out strings.Builder
	ctx.OutputBuffer = &out
	_ = (&TeamCommand{}).Run(ctx, []string{"list"})
	if !strings.Contains(out.String(), "alpha") || !strings.Contains(out.String(), "beta") {
		t.Errorf("list = %q", out.String())
	}
	out.Reset()
	_ = (&TeamCommand{}).Run(ctx, []string{"alpha"})
	out.Reset()
	_ = (&TeamCommand{}).Run(ctx, []string{"status"})
	if !strings.Contains(out.String(), "Team: alpha") {
		t.Errorf("status = %q", out.String())
	}
}

func TestTeamCommand_Off(t *testing.T) {
	cfg := teamCmdConfig()
	ctx := teamCmdContext(t, cfg)
	var out strings.Builder
	ctx.OutputBuffer = &out
	_ = (&TeamCommand{}).Run(ctx, []string{"alpha"})
	out.Reset()
	_ = (&TeamCommand{}).Run(ctx, []string{"off"})
	if !strings.Contains(out.String(), "deactivated") {
		t.Errorf("off = %q", out.String())
	}
	if cfg.Teams.Active != "" {
		t.Errorf("teams.active = %q after off, want empty", cfg.Teams.Active)
	}
}

// Regression (bug: deleting the active team via the picker cleared
// teams.active only in memory — the project LOCAL layer kept the stale value
// and the next start hard-failed validation with "teams.active: team %q not
// defined in teams.definitions"). The picker-delete path must persist the
// cleared selection to the local layer so a cascade reload starts cleanly.
func TestTeamCommand_PickerDeleteActiveClearsPersistedActive(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	projectDir := t.TempDir()

	cfg := teamCmdConfig()
	saver := config.NewCascadeLoader(projectDir, "", nil)
	// Definitions persist to the home config; the reload below resolves
	// teams.active against the on-disk definitions.
	if err := saver.SaveHomeFieldValue([]string{"teams", "definitions"}, cfg.Teams.Definitions); err != nil {
		t.Fatalf("seed home definitions: %v", err)
	}
	// Persist alpha as the active selection in the local layer (as
	// /team:alpha would) before deleting it.
	if err := saver.SaveLocalFieldValue([]string{"teams", "active"}, "alpha"); err != nil {
		t.Fatalf("seed local teams.active: %v", err)
	}

	sess := &stubSession{providerID: "p0", modelID: "m0"}
	m := team.NewManager(cfg, sess, nil, nil, nil, nil)
	ctx := core.Context{
		Config:      cfg,
		TeamManager: m,
		ConfigSaver: saver,
	}
	var out strings.Builder
	ctx.OutputBuffer = &out

	// Activate alpha, then delete it through the picker (selector delete
	// hotkey → confirmation).
	if err := (&TeamCommand{}).Run(ctx, []string{"alpha"}); err != nil {
		t.Fatalf("activate: %v", err)
	}
	var callbacks []func(string, bool)
	ctx.SelectOptionFunc = func(_ string, _ []tui.SelectorItem, _ string, cb func(string, bool)) {
		callbacks = append(callbacks, cb)
	}
	if err := (&TeamCommand{}).Run(ctx, nil); err != nil {
		t.Fatalf("picker: %v", err)
	}
	callbacks[0]("__delete__alpha", true)
	if len(callbacks) != 2 {
		t.Fatalf("callbacks = %d, want selector plus confirmation", len(callbacks))
	}
	callbacks[1]("yes", true)

	if _, ok := cfg.Teams.Definitions["alpha"]; ok {
		t.Fatal("alpha should be removed after picker confirmation")
	}
	if cfg.Teams.Active != "" {
		t.Errorf("teams.active = %q in memory, want cleared", cfg.Teams.Active)
	}

	// The local layer must carry the cleared value — this is what the next
	// start resolves.
	localPath := filepath.Join(projectDir, ".goa", "config.local.yaml")
	localData, err := os.ReadFile(localPath)
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

// Regression: /config → Teams → Active team must persist teams.active to the
// project LOCAL layer (.goa/config.local.yaml), while team definitions stay
// in the home config (a team is a project-scoped, per-developer working set;
// its definitions are user-level).
func TestConfigMenu_TeamsActivePersistsToLocalLayer(t *testing.T) {
	homeDir := t.TempDir()
	t.Setenv("HOME", homeDir)
	t.Setenv("USERPROFILE", homeDir)
	projectDir := t.TempDir()

	cfg := teamCmdConfig()
	sr := &selectRecorder{}
	ctx := &core.Context{
		Config:      cfg,
		ConfigSaver: config.NewCascadeLoader(projectDir, "", nil),
	}
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		sr.title, sr.options, sr.current, sr.onSel = title, options, current, onSelected
	}
	menu := newConfigMenu(*ctx)
	menu.openTeamsActive()
	if sr.title != "Active team:" {
		t.Fatalf("title = %q, want Active team:", sr.title)
	}
	sr.onSel("alpha", true)

	// The local layer carries teams.active.
	localPath := filepath.Join(projectDir, ".goa", "config.local.yaml")
	localData, err := os.ReadFile(localPath)
	if err != nil {
		t.Fatalf("teams.active must be written to %s: %v", localPath, err)
	}
	if !strings.Contains(string(localData), "active: alpha") {
		t.Errorf("local config = %q, want teams.active: alpha", string(localData))
	}

	// Selecting the active team writes ONLY the local layer: the home config
	// must not carry teams.active (it was never written there).
	homePath := filepath.Join(homeDir, ".goa", "config.yaml")
	if homeData, err := os.ReadFile(homePath); err == nil && strings.Contains(string(homeData), "teams") {
		t.Errorf("home config must not carry teams state after active-team selection, got %q", string(homeData))
	}

	// The committed project config must not carry the value either.
	projectPath := filepath.Join(projectDir, ".goa", "config.yaml")
	if projectData, err := os.ReadFile(projectPath); err == nil && strings.Contains(string(projectData), "teams") {
		t.Errorf("project config must not carry teams.active, got %q", string(projectData))
	}
}

func TestConfigMenu_RemoveActiveTeamRefused(t *testing.T) {
	cfg := teamCmdConfig()
	cfg.Teams.Active = "alpha"
	ctx, _, _, _ := newMenuTestContext(t, cfg)
	menu := newConfigMenu(*ctx)
	menu.confirmRemoveTeam("alpha")
	if _, ok := cfg.Teams.Definitions["alpha"]; !ok {
		t.Error("active team must not be removed")
	}
}
