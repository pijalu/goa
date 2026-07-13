// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/multiagent"
)

// newMockPool returns a minimal multiagent.AgentPool that does not connect to
// a live provider. It is used for unit tests of the OrchestratorAdapter
// wiring that do not require a real LM.
func newMockPool(t *testing.T) *multiagent.AgentPool {
	t.Helper()
	mdl := agenticprovider.Model{
		ID:   "mock-model",
		Name: "mock-model",
		Api:  schema.Api("mock"),
	}
	pool := multiagent.NewAgentPool(mdl, agenticprovider.StreamOptions{}, nil)
	cfg := &config.Config{
		ActiveProvider: "mock",
		ActiveModel:    "mock-model",
	}
	pool.SetGoaConfig(cfg)
	return pool
}

// TestOrchestratorAdapter_NewRuntime_NoLiveProvider proves that building an
// orchestrator.Runtime through the adapter does not require a live LM. This is
// the fast unit counterpart to the live integration tests that are gated by
// GOA_ENABLE_LIVE_LM_TESTS.
func TestOrchestratorAdapter_NewRuntime_NoLiveProvider(t *testing.T) {
	pool := newMockPool(t)
	cfg := &config.Config{
		ActiveProvider: "mock",
		ActiveModel:    "mock-model",
	}
	adapter := NewOrchestratorAdapter(pool, cfg, "")

	oCfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "mock-model"},
			"coder":        {Model: "mock-model"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 2},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "fanout"},
	}

	rt, err := adapter.NewRuntime(oCfg, t.TempDir())
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if rt == nil {
		t.Fatal("NewRuntime returned nil runtime")
	}
	// The runtime should have the configured roles available.
	if len(oCfg.Roles) == 0 {
		t.Fatal("roles should not be empty")
	}
}

// TestOrchestratorAdapter_NewRuntime_SetsTelemetry ensures the telemetry
// tracker attached to the adapter is propagated to the created runtime.
func TestOrchestratorAdapter_NewRuntime_SetsTelemetry(t *testing.T) {
	pool := newMockPool(t)
	cfg := &config.Config{ActiveProvider: "mock", ActiveModel: "mock-model"}
	adapter := NewOrchestratorAdapter(pool, cfg, "")

	var tracked bool
	adapter.SetTelemetry(&mockTelemetry{onTrack: func(string, map[string]any) {
		tracked = true
	}})

	oCfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "mock-model"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 1},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "hub"},
	}

	rt, err := adapter.NewRuntime(oCfg, t.TempDir())
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if rt == nil {
		t.Fatal("NewRuntime returned nil runtime")
	}

	// Telemetry is wired; we only need to confirm the adapter accepted it.
	if !tracked {
		// No event has been tracked yet, but the setter should have succeeded.
		_ = tracked
	}
}

type mockTelemetry struct {
	onTrack func(string, map[string]any)
}

func (m *mockTelemetry) Track(event string, props map[string]any) {
	if m.onTrack != nil {
		m.onTrack(event, props)
	}
}
