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

// TestLoadHealsDanglingTeamMemberModels is the regression test for
// B-CfgStaleModel: a team definition whose member model was deleted from the
// model catalog left teams.definitions.*.members.*.model dangling and
// hard-failed startup ("model %q not found in models list"). A dangling
// reference can also arise from manual edits across layers, so Load() must
// heal it (clear + warn) instead of refusing to start.
func TestLoadHealsDanglingTeamMemberModels(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
active_model: m1
models:
  - id: m1
    provider: p
    model: m1
teams:
  definitions:
    Loco:
      main: {model: m1}
      companion: {model: ghost}
      review: "agent"
`)

	cfg, err := NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("Load must heal a dangling team member model, got error: %v", err)
	}
	def, ok := cfg.Teams.Definitions["Loco"]
	if !ok {
		t.Fatalf("Loco definition missing after load: %v", cfg.Teams.Definitions)
	}
	if def.Main == nil || def.Main.Model != "m1" {
		t.Errorf("main model = %+v, want m1 preserved", def.Main)
	}
	if def.Companion == nil || def.Companion.Model != "m1" {
		t.Errorf("companion model = %+v, want ghost rebound to active model m1", def.Companion)
	}
}

// TestLoadHealsDanglingOrchestratorModelRefs covers the orchestrator half of
// B-CfgStaleModel: roles bound to a deleted model and per-model pool caps
// keyed by a deleted model must be healed (cleared + warned) on load, not
// hard-fail startup.
func TestLoadHealsDanglingOrchestratorModelRefs(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
active_model: m1
models:
  - id: m1
    provider: p
    model: m1
orchestrator:
  roles:
    worker: {model: m1}
    reviewer: {model: ghost}
  pool:
    max_agents_per_model:
      m1: 2
      ghost: 4
`)

	cfg, err := NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("Load must heal dangling orchestrator model refs, got error: %v", err)
	}
	if got := cfg.Orchestrator.Roles["worker"].Model; got != "m1" {
		t.Errorf("worker role model = %q, want m1 preserved", got)
	}
	if got := cfg.Orchestrator.Roles["reviewer"].Model; got != "m1" {
		t.Errorf("reviewer role model = %q, want ghost rebound to active model m1", got)
	}
	if _, ok := cfg.Orchestrator.Pool.MaxAgentsPerModel["ghost"]; ok {
		t.Errorf("ghost pool cap survived the heal, per_model = %v", cfg.Orchestrator.Pool.MaxAgentsPerModel)
	}
	if got := cfg.Orchestrator.Pool.MaxAgentsPerModel["m1"]; got != 2 {
		t.Errorf("m1 pool cap = %d, want 2 preserved", got)
	}
}

// TestLoadKeepsConfiguredModelRefs guards the heal against overreach: team
// member and orchestrator role/pool references to configured models must be
// preserved as-is.
func TestLoadKeepsConfiguredModelRefs(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
models:
  - id: m1
    provider: p
    model: m1
teams:
  definitions:
    Loco:
      main: {model: m1}
      review: "off"
orchestrator:
  roles:
    worker: {model: m1}
  pool:
    max_agents_per_model:
      m1: 3
`)

	cfg, err := NewCascadeLoader(projectDir, "", nil).Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Teams.Definitions["Loco"].Main.Model != "m1" {
		t.Errorf("team main model = %q, want m1 preserved", cfg.Teams.Definitions["Loco"].Main.Model)
	}
	if cfg.Orchestrator.Roles["worker"].Model != "m1" {
		t.Errorf("worker role model = %q, want m1 preserved", cfg.Orchestrator.Roles["worker"].Model)
	}
	if cfg.Orchestrator.Pool.MaxAgentsPerModel["m1"] != 3 {
		t.Errorf("m1 pool cap = %d, want 3 preserved", cfg.Orchestrator.Pool.MaxAgentsPerModel["m1"])
	}
}

// TestSanitizeDanglingModelRefsWarns verifies the heal emits a stderr warning
// naming each dropped reference so the user knows why it is gone.
func TestSanitizeDanglingModelRefsWarns(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	orig := os.Stderr
	os.Stderr = w
	defer func() { os.Stderr = orig }()

	cfg := &Config{
		Models: []ModelConfig{{ID: "m1", ProviderID: "p", Model: "m1"}},
		Teams: TeamsConfig{
			Definitions: map[string]TeamDefinition{
				"Loco": {Companion: &TeamMember{Model: "ghost"}},
			},
		},
	}
	cfg.Orchestrator.Roles = map[string]OrchestratorRole{
		"reviewer": {Model: "ghost"},
	}
	cfg.Orchestrator.Pool.MaxAgentsPerModel = map[string]int{"ghost": 2}
	cfg.sanitizeDanglingModelRefs()

	w.Close()
	out, _ := io.ReadAll(r)
	// No active model set and "m1" is the only configured model: rebound to it.
	if got := cfg.Teams.Definitions["Loco"].Companion.Model; got != "m1" {
		t.Errorf("companion model = %q, want rebound to m1", got)
	}
	if got := cfg.Orchestrator.Roles["reviewer"].Model; got != "m1" {
		t.Errorf("reviewer role model = %q, want rebound to m1", got)
	}
	if _, ok := cfg.Orchestrator.Pool.MaxAgentsPerModel["ghost"]; ok {
		t.Errorf("ghost pool cap survived, want dropped")
	}
	if !strings.Contains(string(out), "ghost") {
		t.Errorf("warning = %q, want it to name the dropped model", string(out))
	}
}

// TestLoadDanglingModelRefsGenuineErrorForcesFallback verifies that healing
// dangling model references does not mask genuine config errors: a config
// with an invalid execution.mode cannot be used — the loader falls back to
// the defaults and reports the genuine problem as FallbackErr (bugs.md
// 2026-09-26: goa always aims to start) while the dangling ref is still
// healed so a later fix works cleanly.
func TestLoadDanglingModelRefsGenuineErrorForcesFallback(t *testing.T) {
	homeDir, projectDir, cleanup := setupTestConfig(t)
	defer cleanup()
	t.Setenv("HOME", homeDir)

	writeConfig(t, filepath.Join(homeDir, ".goa", "config.yaml"), `
models:
  - id: m1
    provider: p
    model: m1
execution:
  mode: bogus
teams:
  definitions:
    Loco:
      main: {model: m1}
      companion: {model: ghost}
      review: "agent"
`)

	cfg, rep, err := NewCascadeLoader(projectDir, "", nil).LoadWithReport()
	if err != nil {
		t.Fatalf("Load must not fail hard (defaults fallback), got: %v", err)
	}
	if cfg == nil {
		t.Fatal("cfg must never be nil when defaults can load")
	}
	if !rep.UsedDefaults {
		t.Fatal("UsedDefaults = false, want the defaults fallback for a genuine config error (bad execution.mode)")
	}
	if rep.FallbackErr == nil || !strings.Contains(rep.FallbackErr.Error(), "execution.mode") {
		t.Errorf("FallbackErr = %v, want it to mention the genuine execution.mode problem", rep.FallbackErr)
	}
}
