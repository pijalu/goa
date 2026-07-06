// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/core/orchestrator"
)

// TestOrchestrateCommand_LiveHubGoal is the capstone end-to-end validation:
// `/orchestrate new hub goal <obj> <obj>` runs against live LMStudio, where the
// orchestrator agent delegates to the coder via the DelegateTool, aggregate
// token usage accrues to a bound goal, and the goal is marked complete on a
// successful finish. Skips without a local model.
func TestOrchestrateCommand_LiveHubGoal(t *testing.T) {
	if !lmstudioReachable(t) {
		t.Skip("LMStudio not reachable on :1234 — skipping live hub+goal test")
	}
	cfg, _, pool := loadLiveConfig(t)
	rootDir := t.TempDir()
	adapter := NewOrchestratorAdapter(pool, cfg)
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &commands.OrchestrateCommand{
		Builder:  adapter,
		Active:   orchestrator.NewActiveRuntime(),
		RootDir:  rootDir,
		GoalMode: mode,
	}
	cfg.Orchestrator = config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: cfg.ActiveModel},
			"coder":        {Model: cfg.ActiveModel},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 2},
		Defaults: config.OrchestratorDefaultsConfig{Topology: config.OrchestratorTopologyHub},
	}
	ctx := core.Context{Config: cfg}

	obj := "You must use the 'delegate' tool to ask the 'coder' role to reply with the single word 'ready'. Then summarize."
	if err := cmd.Run(ctx, []string{"new", "topology=hub", "objective=" + obj}); err != nil {
		t.Fatalf("/orchestrate new hub goal: %v", err)
	}

	waitForActiveClear(cmd.Active, 100*time.Second, t)

	// Goal must have been created and then completed (complete clears active goal).
	if snap := mode.GetActiveGoal(); snap != nil {
		t.Errorf("goal should be cleared after successful run; got status=%s tokens=%d",
			snap.Status, snap.TokensUsed)
	}

	runs, _ := orchestrator.ListRuns(rootDir)
	if len(runs) != 1 || !runs[0].Finished {
		t.Fatalf("expected 1 finished persisted run, got %+v", runs)
	}
}
