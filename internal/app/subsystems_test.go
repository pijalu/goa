// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/prompts"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tools"
)

func TestPopulateModeDefaults_LoadsFromRegistry(t *testing.T) {
	cfg := &config.Config{}
	reg := core.NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))

	populateModeDefaults(cfg, reg)

	if cfg.Mode.Defaults == nil {
		t.Fatal("expected Mode.Defaults to be initialized")
	}
	if cfg.Mode.Defaults["coding-posture"] != internal.AutonomySolo {
		t.Errorf("coding-posture default = %q, want %q", cfg.Mode.Defaults["coding-posture"], internal.AutonomySolo)
	}
	if cfg.Mode.Defaults[internal.MajorPlanner] != internal.AutonomyReview {
		t.Errorf("planner default = %q, want %q", cfg.Mode.Defaults[internal.MajorPlanner], internal.AutonomyReview)
	}
}

func TestPopulateModeDefaults_PreservesExisting(t *testing.T) {
	cfg := &config.Config{}
	cfg.Mode.Defaults = map[internal.MajorMode]internal.AutonomyLevel{
		internal.MajorPlanner: internal.AutonomySolo,
	}
	reg := core.NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))

	populateModeDefaults(cfg, reg)

	if cfg.Mode.Defaults[internal.MajorPlanner] != internal.AutonomySolo {
		t.Errorf("existing planner default overwritten: got %q", cfg.Mode.Defaults[internal.MajorPlanner])
	}
}

func TestRegisterGoalTools_GatedByConfigAndFlag(t *testing.T) {
	cfg := &config.Config{}
	if goalToolsEnabled(cfg, RuntimeOptions{}) {
		t.Error("goal tools should be disabled by default")
	}
	if !goalToolsEnabled(cfg, RuntimeOptions{Goal: true}) {
		t.Error("--goal flag should force-enable goal tools")
	}

	cfg2 := &config.Config{}
	cfg2.Tools.Enabled.SetEnabled("goal", true)
	if !goalToolsEnabled(cfg2, RuntimeOptions{}) {
		t.Error("tools.enabled.goal=true should enable goal tools")
	}
}

// goalToolsEnabled mirrors the gate used in InitSubsystems.
func goalToolsEnabled(cfg *config.Config, opts RuntimeOptions) bool {
	return cfg.Tools.Enabled.Goal || opts.Goal
}

// TestRegisterGoalTools_Directly verifies the helper registers the expected
// goal-related tools.
func TestRegisterGoalTools_Directly(t *testing.T) {
	dir := t.TempDir()
	gm := core.NewGoalManager(dir)
	reg := tools.NewToolRegistry()
	registerGoalTools(reg, gm)
	for _, name := range []string{"CreateGoal", "UpdateGoal", "GetGoal"} {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("expected %q tool to be registered", name)
		}
	}
}

func TestRegisterSkillRunnerIfNeeded_RegistersForSubAgentMode(t *testing.T) {
	skillReg := skills.NewSkillRegistry(nil)
	skillReg.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	_ = skillReg.LoadAll()
	pool := multiagent.NewAgentPool(provider.Model{}, provider.StreamOptions{}, nil)
	promptReg := prompts.NewRegistry(prompts.EmbeddedFS())
	toolReg := tools.NewToolRegistry()

	cfg := &config.Config{Skills: config.SkillsConfig{ExecutionMode: config.AgenticSkillModeSubAgent}}
	registerSkillRunnerIfNeeded(toolReg, skillReg, pool, promptReg, cfg)
	if _, ok := toolReg.Get("run_skill"); !ok {
		t.Error("expected run_skill tool to be registered in subagent mode")
	}

	toolReg2 := tools.NewToolRegistry()
	cfg2 := &config.Config{Skills: config.SkillsConfig{ExecutionMode: config.AgenticSkillModeInline}}
	registerSkillRunnerIfNeeded(toolReg2, skillReg, pool, promptReg, cfg2)
	if _, ok := toolReg2.Get("run_skill"); ok {
		t.Error("expected run_skill tool NOT to be registered in inline mode")
	}
}
