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

// TestRegisterGoalTools_Directly verifies the helper registers the single
// unified goal tool (bugs.md S2: one `goal` tool, always registered).
func TestRegisterGoalTools_Directly(t *testing.T) {
	dir := t.TempDir()
	gm := core.NewGoalManager(dir)
	reg := tools.NewToolRegistry()
	registerGoalTools(reg, gm, false)
	if _, ok := reg.Get("goal"); !ok {
		t.Errorf("expected the unified \"goal\" tool to be registered")
	}
	// The four legacy per-action tools must no longer be registered.
	for _, name := range []string{"CreateGoal", "UpdateGoal", "GetGoal", "SetGoalBudget"} {
		if _, ok := reg.Get(name); ok {
			t.Errorf("legacy tool %q must not be registered after consolidation", name)
		}
	}
}

// TestRegisterSubAgentTools_GatedByConfig verifies agent/agent_swarm are
// registered by default and omitted when disabled via tools.enabled.
func TestRegisterSubAgentTools_GatedByConfig(t *testing.T) {
	newRegistry := func() *core.ModeRegistry {
		return core.NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS()))
	}

	// enabled (default): both tools registered
	toolReg := tools.NewToolRegistry()
	cfg := &config.Config{}
	cfg.Tools.Enabled.SetEnabled("agent", true)
	cfg.Tools.Enabled.SetEnabled("agent_swarm", true)
	registerSubAgentTools(toolReg, nil, newRegistry(), nil, nil, nil, nil, cfg)
	if _, ok := toolReg.Get("agent"); !ok {
		t.Error("expected agent tool to be registered when enabled")
	}
	if _, ok := toolReg.Get("agent_swarm"); !ok {
		t.Error("expected agent_swarm tool to be registered when enabled")
	}

	// disabled: neither tool registered
	toolReg2 := tools.NewToolRegistry()
	cfg2 := &config.Config{}
	cfg2.Tools.Enabled.SetEnabled("agent", false)
	cfg2.Tools.Enabled.SetEnabled("agent_swarm", false)
	registerSubAgentTools(toolReg2, nil, newRegistry(), nil, nil, nil, nil, cfg2)
	if _, ok := toolReg2.Get("agent"); ok {
		t.Error("expected agent tool NOT to be registered when disabled")
	}
	if _, ok := toolReg2.Get("agent_swarm"); ok {
		t.Error("expected agent_swarm tool NOT to be registered when disabled")
	}
}

// TestRegisterSubAgentTools_IndependentToggles verifies agent and agent_swarm
// can be toggled independently.
func TestRegisterSubAgentTools_IndependentToggles(t *testing.T) {
	toolReg := tools.NewToolRegistry()
	cfg := &config.Config{}
	cfg.Tools.Enabled.SetEnabled("agent", true)
	cfg.Tools.Enabled.SetEnabled("agent_swarm", false)
	registerSubAgentTools(toolReg, nil, core.NewModeRegistry(prompts.NewRegistry(prompts.EmbeddedFS())), nil, nil, nil, nil, cfg)
	if _, ok := toolReg.Get("agent"); !ok {
		t.Error("expected agent tool to be registered")
	}
	if _, ok := toolReg.Get("agent_swarm"); ok {
		t.Error("expected agent_swarm tool NOT to be registered")
	}
}

// TestRegisterSkillRunner_RegistersForBothModes verifies the run_skill tool
// is registered in every execution mode: in sub-agent mode it spawns a
// sub-agent, in inline mode it returns the skill body as its tool result.
// The mode is carried on the tool so Execute can dispatch accordingly.
func TestRegisterSkillRunner_RegistersForBothModes(t *testing.T) {
	skillReg := skills.NewSkillRegistry(nil)
	skillReg.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	_ = skillReg.LoadAll()
	pool := multiagent.NewAgentPool(provider.Model{}, provider.StreamOptions{}, nil)
	promptReg := prompts.NewRegistry(prompts.EmbeddedFS())

	toolReg := tools.NewToolRegistry()
	cfg := &config.Config{Skills: config.SkillsConfig{ExecutionMode: config.AgenticSkillModeSubAgent}}
	registerSkillRunner(toolReg, skillReg, pool, promptReg, cfg)
	subTool, ok := toolReg.Get("run_skill")
	if !ok {
		t.Fatal("expected run_skill tool to be registered in subagent mode")
	}
	if rt, ok := subTool.(*skills.SkillRunnerTool); !ok || rt.Inline {
		t.Errorf("subagent mode: expected SkillRunnerTool with Inline=false, got %T", subTool)
	}

	toolReg2 := tools.NewToolRegistry()
	cfg2 := &config.Config{Skills: config.SkillsConfig{ExecutionMode: config.AgenticSkillModeInline}}
	registerSkillRunner(toolReg2, skillReg, pool, promptReg, cfg2)
	inlineTool, ok := toolReg2.Get("run_skill")
	if !ok {
		t.Fatal("expected run_skill tool to be registered in inline mode")
	}
	if rt, ok := inlineTool.(*skills.SkillRunnerTool); !ok || !rt.Inline {
		t.Errorf("inline mode: expected SkillRunnerTool with Inline=true, got %T", inlineTool)
	}
}
