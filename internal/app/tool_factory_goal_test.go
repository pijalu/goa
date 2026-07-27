// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
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

// Regression for bugs.md: "/tools:goal:on reports success but the goal tool
// still errors — ✗ ◆ Started goal Phase 1...". A successful create through the
// runtime-built tool must return a nil error so the renderer draws ✓, not ✗.
func TestMakeToolFactory_Goal_CreateIsNotErrorFlagged(t *testing.T) {
	cfg := &config.Config{}
	cfg.Tools.Enabled.SetEnabled("goal", true)
	gm := core.NewGoalManager(t.TempDir())
	subs := &subsystems{cfg: cfg, goalManager: gm}

	factory := makeToolFactory(subs)
	tool, ok := factory("goal")
	if !ok || tool == nil {
		t.Fatalf("factory did not build goal tool: ok=%v tool=%v", ok, tool)
	}
	out, err := tool.Execute(`{"action":"create","objective":"Phase 1: do the thing"}`)
	if err != nil {
		t.Fatalf("goal create returned a non-nil error (renders ✗ on a success body): %v", err)
	}
	if !strings.Contains(out, "goal") {
		t.Errorf("create output missing goal payload: %q", out)
	}
}

// TestNewGoalTool_QueueWired verifies both the startup and runtime construction
// paths wire the durable goal queue into the tool, so the model can manage
// goals as a todo-like list (create appends by default; list/cancel/reorder).
func TestNewGoalTool_QueueWired(t *testing.T) {
	gm := core.NewGoalManager(t.TempDir())
	tool := newGoalTool(gm, true, nil, nil)
	// Behavioural check: create two goals — the second must queue, which
	// requires the queue to be wired into the tool.
	if _, err := tool.Execute(`{"action":"create","objective":"first"}`); err != nil {
		t.Fatalf("create first: %v", err)
	}
	out, err := tool.Execute(`{"action":"create","objective":"second"}`)
	if err != nil {
		t.Fatalf("create second should append to queue (queue must be wired), got: %v", err)
	}
	if !strings.Contains(out, `"queued":1`) {
		t.Errorf("expected second goal queued (queue wired), got %q", out)
	}
	// list reflects active + queued.
	listOut, err := tool.Execute(`{"action":"list"}`)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(listOut, "first") || !strings.Contains(listOut, "second") {
		t.Errorf("list output missing goals: %q", listOut)
	}
}
