// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
)

// Regression for bugs.md: /tools:goal:on reported "Tool goal could not be
// instantiated at runtime. Restart Goa to apply the change." because
// makeToolFactory had no "goal" case. The factory must build the goal tool
// with the same GoalMode wiring used at startup.
func TestMakeToolFactory_Goal_Instantiates(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Enabled.SetEnabled("goal", true)
	gm := core.NewGoalManager(t.TempDir())
	subs := &subsystems{cfg: cfg, goalManager: gm}

	factory := makeToolFactory(subs)
	tool, ok := factory("goal")
	if !ok {
		t.Fatal("factory returned ok=false for goal — tool could not be instantiated at runtime")
	}
	if tool == nil {
		t.Fatal("factory returned nil tool for goal")
	}
	if tool.Schema().Name != "goal" {
		t.Errorf("tool name = %q, want goal", tool.Schema().Name)
	}
}

// The goal tool built at runtime must be functional: a get action against the
// bound GoalMode should succeed (proving Mode is wired, not nil).
func TestMakeToolFactory_Goal_ToolIsFunctional(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Enabled.SetEnabled("goal", true)
	gm := core.NewGoalManager(t.TempDir())
	subs := &subsystems{cfg: cfg, goalManager: gm}

	factory := makeToolFactory(subs)
	tool, ok := factory("goal")
	if !ok || tool == nil {
		t.Fatalf("factory did not build goal tool: ok=%v tool=%v", ok, tool)
	}
	// "get" with no goal returns a valid (empty) result rather than panicking
	// on a nil Mode.
	if _, err := tool.Execute(`{"action":"get"}`); err != nil {
		t.Errorf("goal tool get failed (Mode not wired?): %v", err)
	}
}
