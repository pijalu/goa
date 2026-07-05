// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"net/http"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/core/orchestrator"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/provider"
)

// TestOrchestrateCommand_LiveNewRun drives the real /orchestrate new command
// path against LMStudio: command → adapter → bounded pool → live agents →
// events → active-runtime cleared. Skips when no local model is reachable.
func TestOrchestrateCommand_LiveNewRun(t *testing.T) {
	if !lmstudioReachable(t) {
		t.Skip("LMStudio not reachable on :1234 — skipping live /orchestrate test")
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
	cmd := &commands.OrchestrateCommand{
		Builder: adapter,
		Active:  orchestrator.NewActiveRuntime(),
		RootDir: rootDir,
	}

	// Configure two roles on the active model and a fanout run.
	cfg.Orchestrator = config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"summarizer": {Model: cfg.ActiveModel},
			"coder":      {Model: cfg.ActiveModel},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 2},
		Defaults: config.OrchestratorDefaultsConfig{Topology: config.OrchestratorTopologyFanout},
	}
	ctx := core.Context{Config: cfg}

	if err := cmd.Run(ctx, []string{"new", "fanout", "Reply with the single word: ready"}); err != nil {
		t.Fatalf("/orchestrate new: %v", err)
	}

	// Wait for the run goroutine to finish and clear the active holder.
	deadline := time.Now().Add(90 * time.Second)
	for time.Now().Before(deadline) {
		if cmd.Active.Get() == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if cmd.Active.Get() != nil {
		t.Fatalf("/orchestrate new run did not complete within timeout")
	}

	// The run must have persisted exactly one resumable run on disk.
	runs, err := orchestrator.ListRuns(rootDir)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if len(runs) != 1 {
		t.Fatalf("expected 1 persisted run, got %d", len(runs))
	}
	snap, err := orchestrator.ReplaySnapshot(orchestrator.NewFileEventStore(rootDir, runs[0].RunID))
	if err != nil {
		t.Fatalf("ReplaySnapshot: %v", err)
	}
	if !snap.Finished || len(snap.Agents) != 2 {
		t.Fatalf("snapshot: finished=%v agents=%d", snap.Finished, len(snap.Agents))
	}
	for id, a := range snap.Agents {
		if a.Status != orchestrator.AgentFinished {
			t.Errorf("agent %s status=%q want finished", id, a.Status)
		}
	}
}

// keep the http import meaningful if lmstudioReachable ever moves.
var _ = http.StatusOK
