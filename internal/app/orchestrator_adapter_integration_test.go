// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core/orchestrator"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/provider"
)

// lmstudioReachable reports whether a local LMStudio server is up. The
// orchestrator integration test auto-skips when it is not, so this test runs
// in developer environments (where LMStudio is available) and is a no-op in CI.
func lmstudioReachable(t *testing.T) bool {
	t.Helper()
	client := http.Client{Timeout: 1500 * time.Millisecond}
	resp, err := client.Get("http://localhost:1234/v1/models")
	if err != nil {
		return false
	}
	resp.Body.Close()
	return resp.StatusCode == http.StatusOK
}

// TestOrchestratorAdapter_LiveFanout drives a real fanout orchestration
// against LMStudio and asserts the full lifecycle: bounded pool admits both
// agents, real turns stream, token stats are captured, events are persisted,
// and the replayed snapshot marks the run finished.
//
// It is skipped (not failed) when LMStudio is unreachable or no model is
// configured, so the gate suite stays green on machines without a local model.
func TestOrchestratorAdapter_LiveFanout(t *testing.T) {
	if !lmstudioReachable(t) {
		t.Skip("LMStudio not reachable on :1234 — skipping live orchestrator integration test")
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
			"summarizer": {Model: cfg.ActiveModel},
			"coder":      {Model: cfg.ActiveModel},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 2},
		Defaults: config.OrchestratorDefaultsConfig{Topology: config.OrchestratorTopologyFanout},
	}
	adapter := NewOrchestratorAdapter(pool, cfg)
	rt, err := adapter.NewRuntime(oCfg, rootDir)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}

	// Collect lifecycle event types as the run progresses.
	var got []orchestrator.EventType
	done := make(chan struct{})
	go func() {
		defer close(done)
		for ev := range rt.Events() {
			got = append(got, ev.Type)
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	if err := rt.Run(ctx, "Reply with the single word: ready"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	<-done

	have := map[orchestrator.EventType]bool{}
	for _, e := range got {
		have[e] = true
	}
	for _, want := range []orchestrator.EventType{
		orchestrator.EventRunStarted, orchestrator.EventAgentStarted,
		orchestrator.EventAgentStats, orchestrator.EventAgentFinished,
		orchestrator.EventRunFinished,
	} {
		if !have[want] {
			t.Errorf("missing event %s; got %v", want, got)
		}
	}

	runs, err := orchestrator.ListRuns(rootDir)
	if err != nil || len(runs) != 1 {
		t.Fatalf("ListRuns: %v (len=%d)", err, len(runs))
	}
	snap, err := orchestrator.ReplaySnapshot(orchestrator.NewFileEventStore(rootDir, runs[0].RunID))
	if err != nil {
		t.Fatalf("ReplaySnapshot: %v", err)
	}
	if !snap.Finished {
		t.Errorf("snapshot not finished")
	}
	if len(snap.Agents) != 2 {
		t.Fatalf("snapshot agents = %d, want 2", len(snap.Agents))
	}
	for id, a := range snap.Agents {
		if a.Status != orchestrator.AgentFinished {
			t.Errorf("agent %s status = %q, want finished", id, a.Status)
		}
		// Token stats must have been captured from the live model.
		if a.TokensOut == 0 {
			t.Errorf("agent %s captured zero output tokens (stats observer not wired)", id)
		}
	}
	// Clean up the persisted run dir the adapter wrote.
	_ = os.RemoveAll(rootDir)
}
