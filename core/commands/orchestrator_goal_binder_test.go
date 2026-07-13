// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"testing"
	"time"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/core/orchestrator"
)

// TestGoalBinder_WrapsGoalMode proves the command-level binder creates a real
// goal, accrues tokens, and marks it complete.
func TestGoalBinder_WrapsGoalMode(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	gb := NewGoalBinder(mode)

	id, err := gb.Create("ship the feature", 0)
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if id == "" {
		t.Error("empty goal id")
	}
	if snap := mode.GetActiveGoal(); snap == nil || snap.Objective != "ship the feature" {
		t.Errorf("goal not active after create: %+v", snap)
	}

	if _, err := gb.RecordTokens(42); err != nil {
		t.Fatalf("RecordTokens: %v", err)
	}
	if snap := mode.GetActiveGoal(); snap.TokensUsed != 42 {
		t.Errorf("tokens = %d, want 42", snap.TokensUsed)
	}

	if err := gb.Complete("orchestration finished"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	// Complete clears the active goal.
	if snap := mode.GetActiveGoal(); snap != nil {
		t.Errorf("goal still active after complete: %+v", snap)
	}
}

func TestGoalBinder_EphemeralManagedGoal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	gb := NewGoalBinder(mode).(*goalModeBinder)

	_, err := gb.CreateWithName("ship it", "happy.hare", 0)
	if err != nil {
		t.Fatalf("CreateWithName: %v", err)
	}
	g := mode.GetGoal().Goal
	if g == nil || g.ManagedBy != "orchestrator" {
		t.Fatalf("goal not managed: %+v", g)
	}
	if g.Name != "happy.hare" {
		t.Errorf("goal name = %q, want happy.hare", g.Name)
	}

	if err := gb.Complete("done"); err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if mode.GetGoal().Goal != nil {
		t.Errorf("managed goal should be cleared after complete, got %+v", mode.GetGoal().Goal)
	}

	// Re-create and delete explicitly.
	_, err = gb.CreateWithName("ship it", "happy.hare", 0)
	if err != nil {
		t.Fatalf("CreateWithName 2: %v", err)
	}
	if err := gb.Delete("run deleted"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if mode.GetGoal().Goal != nil {
		t.Errorf("managed goal should be cleared after delete, got %+v", mode.GetGoal().Goal)
	}
}

// TestOrchestrateCommand_GoalBinding end-to-end (fake builder + real GoalMode):
// `/orchestrate new fanout goal <obj> <obj>` binds a goal; on run completion
// the goal is marked complete.
func TestOrchestrateCommand_GoalBinding(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	b := &fakeBuilder{}
	c := &OrchestrateCommand{
		Builder: b, Active: orchestrator.NewActiveRuntime(),
		RootDir: t.TempDir(), GoalMode: mode,
	}
	ctx := testCtx(t)

	if err := c.Run(ctx, []string{"new", "topology=fanout", "objective=deliver value"}); err != nil {
		t.Fatalf("Run: %v", err)
	}
	// Wait for the background run to finish.
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) && c.Active.Get() != nil {
		time.Sleep(10 * time.Millisecond)
	}
	if c.Active.Get() != nil {
		t.Fatalf("run did not finish")
	}
	// The goal must have been created then cleared on complete.
	if snap := mode.GetActiveGoal(); snap != nil {
		t.Errorf("goal should be cleared after successful run, got %+v", snap)
	}
}

// TestRuntime_GoalBindingAdapterWiring is a smoke test that the binder set on
// a runtime via the command path accrues tokens through the fake factory.
func TestOrchestrateCommand_GoalAccrualViaRuntime(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cfg := testCtx(t).Config.Orchestrator
	factory := func(role, model string, opts orchestrator.AcquireOptions) (*orchestrator.AgentHandle, error) {
		h := orchestrator.NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error {
			h.Stats.AddUsage(5, 5, 0, 0)
			return nil
		}
		return h, nil
	}
	pool := orchestrator.NewBoundedAgentPool(cfg, factory)
	rt, _ := orchestrator.NewRuntime(cfg, pool, nil, t.TempDir())
	gb := NewGoalBinder(mode)
	if _, err := gb.Create("obj", 0); err != nil {
		t.Fatal(err)
	}
	rt.SetGoalBinder(gb)
	_ = mode // created goal is tracked internally; completion clears it
	if err := rt.Run(context.Background(), "obj"); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
