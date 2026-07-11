// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"net/http"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core/orchestrator"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/provider"
)

// lmstudioReachable reports whether a local LMStudio server is up. Live
// orchestrator integration tests auto-skip when it is not, so they run in
// developer environments and are no-ops in CI.
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

// loadLiveConfig loads the project config, skips when no local model is active,
// and returns a ready provider manager and model pool for live integration tests.
func loadLiveConfig(t *testing.T) (*config.Config, *provider.ProviderManager, *multiagent.AgentPool) {
	t.Helper()
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
	return cfg, pm, pool
}

// buildOrchestratorConfig creates a live orchestrator config with the given
// role names mapped to the active model.
func buildOrchestratorConfig(cfg *config.Config, roles []string, topology string) config.OrchestratorConfig {
	roleMap := make(map[string]config.OrchestratorRole, len(roles))
	for _, r := range roles {
		roleMap[r] = config.OrchestratorRole{Model: cfg.ActiveModel}
	}
	return config.OrchestratorConfig{
		Roles:    roleMap,
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: len(roles)},
		Defaults: config.OrchestratorDefaultsConfig{Topology: topology},
	}
}

// newLiveRuntime builds an orchestrator.Runtime backed by a live pool for the
// given roles and topology, using a fresh temp directory.
func newLiveRuntime(t *testing.T, roles []string, topology string) (*orchestrator.Runtime, string) {
	t.Helper()
	cfg, _, pool := loadLiveConfig(t)
	oCfg := buildOrchestratorConfig(cfg, roles, topology)
	adapter := NewOrchestratorAdapter(pool, cfg, "")
	rootDir := t.TempDir()
	rt, err := adapter.NewRuntime(oCfg, rootDir)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	return rt, rootDir
}

// assertRunSnapshotFinished validates that the single persisted run in rootDir
// is finished and contains exactly the expected number of finished agents.
func assertRunSnapshotFinished(t *testing.T, rootDir string, wantAgents int) *orchestrator.RunSnapshot {
	t.Helper()
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
	if !snap.Finished {
		t.Errorf("snapshot not finished")
	}
	if len(snap.Agents) != wantAgents {
		t.Fatalf("snapshot agents = %d, want %d", len(snap.Agents), wantAgents)
	}
	for id, a := range snap.Agents {
		if a.Status != orchestrator.AgentFinished {
			t.Errorf("agent %s status = %q, want finished", id, a.Status)
		}
	}
	return snap
}
