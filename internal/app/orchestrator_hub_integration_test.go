// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core/orchestrator"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/provider"
)

// TestOrchestratorAdapter_LiveHub drives a TRUE hub topology against LMStudio:
// the orchestrator agent is given the DelegateTool and must call it to dispatch
// a sub-task to the coder specialist. Asserts both the orchestrator and the
// delegated coder appear in the replayed snapshot, proving real tool-driven
// delegation end-to-end. Skips when no local model is reachable.
func TestOrchestratorAdapter_LiveHub(t *testing.T) {
	if !lmstudioReachable(t) {
		t.Skip("LMStudio not reachable on :1234 — skipping live hub test")
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
	oCfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: cfg.ActiveModel},
			"coder":        {Model: cfg.ActiveModel},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 2},
		Defaults: config.OrchestratorDefaultsConfig{Topology: config.OrchestratorTopologyHub},
	}
	adapter := NewOrchestratorAdapter(pool, cfg)
	rt, err := adapter.NewRuntime(oCfg, rootDir)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}

	done := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
		defer cancel()
		done <- rt.Run(ctx,
			"You must use the 'delegate' tool to ask the 'coder' role: \"Reply with the single word: ready\". "+
				"After it returns, summarize its answer in one sentence.")
	}()

	// Drain events so the bus does not block; count agent starts by role.
	var mu sync.Mutex
	started := map[string]int{}
	go func() {
		for ev := range rt.Events() {
			if ev.Type == orchestrator.EventAgentStarted {
				mu.Lock()
				started[ev.Role]++
				mu.Unlock()
			}
		}
	}()

	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("hub Run: %v", err)
		}
	case <-time.After(100 * time.Second):
		t.Fatalf("hub run timed out")
	}

	// The orchestrator must have started, AND the coder must have been
	// delegated to (started via the DelegateTool). This is the proof of real
	// tool-driven delegation rather than the orchestrator just answering alone.
	mu.Lock()
	orcStarted := started["orchestrator"]
	coderStarted := started["coder"]
	mu.Unlock()
	if orcStarted == 0 {
		t.Errorf("orchestrator agent never started")
	}
	if coderStarted == 0 {
		t.Errorf("coder was never delegated to — orchestrator did not use the delegate tool; started=%v", started)
	}

	runs, _ := orchestrator.ListRuns(rootDir)
	if len(runs) != 1 {
		t.Fatalf("expected 1 persisted run, got %d", len(runs))
	}
	snap, err := orchestrator.ReplaySnapshot(orchestrator.NewFileEventStore(rootDir, runs[0].RunID))
	if err != nil {
		t.Fatalf("ReplaySnapshot: %v", err)
	}
	if !snap.Finished {
		t.Errorf("snapshot not finished")
	}
}
