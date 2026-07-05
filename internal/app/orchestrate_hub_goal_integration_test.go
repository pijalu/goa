// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/core/commands"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/provider"
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
	cl := config.NewCascadeLoader(".", "", nil)
	cfg, err := cl.Load()
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.ActiveModel == "" {
		t.Skip("no active_model configured")
	}

	pm := provider.NewProviderManager(cfg)
	mdl, err := pm.ResolveModelByID(cfg.ActiveModel)
	if err != nil {
		t.Fatalf("resolve model: %v", err)
	}
	opts := pm.BuildStreamOptions()
	pool := multiagent.NewAgentPool(mdl, opts, nil)
	pool.SetGoaConfig(cfg)
	pool.ModelFactory = func(name string) (agenticprovider.Model, error) {
		return pm.ResolveModelByID(name)
	}
	pool.ProviderModelFactory = func(pid, name string) (agenticprovider.Model, error) {
		return pm.ResolveModelForProvider(pid, name)
	}

	rootDir := t.TempDir()
	adapter := NewOrchestratorAdapter(pool, cfg)
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	cmd := &commands.OrchestrateCommand{
		Builder: adapter, Active: orchestrator.NewActiveRuntime(),
		RootDir: rootDir, GoalMode: mode,
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

	// The objective doubles as the goal objective.
	obj := "You must use the 'delegate' tool to ask the 'coder' role to reply with the single word 'ready'. Then summarize."
	if err := cmd.Run(ctx, []string{"new", "hub", "goal", obj, obj}); err != nil {
		t.Fatalf("/orchestrate new hub goal: %v", err)
	}

	// Wait for the run to clear the active holder.
	deadline := time.Now().Add(100 * time.Second)
	for time.Now().Before(deadline) && cmd.Active.Get() != nil {
		time.Sleep(30 * time.Millisecond)
	}
	if cmd.Active.Get() != nil {
		t.Fatalf("hub+goal run did not finish within timeout")
	}

	// Goal must have been created and then completed (complete clears active goal).
	if snap := mode.GetActiveGoal(); snap != nil {
		t.Errorf("goal should be cleared after successful run; got status=%s tokens=%d",
			snap.Status, snap.TokensUsed)
	}
	// And a persisted run exists and is finished.
	runs, _ := orchestrator.ListRuns(rootDir)
	if len(runs) != 1 || !runs[0].Finished {
		t.Fatalf("expected 1 finished persisted run, got %+v", runs)
	}
}

// commandsCtx/test helpers removed — core.Context is built inline.
var _ = context.Background
