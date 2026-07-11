// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
)

// TestOrchestratorDelegateTool_DefaultReusesAndNewAgent verifies the delegate
// tool's two delegation modes:
//   - Default (no new_agent): the task is passed verbatim and Fresh=false so
//     the harness reuses the role's pooled specialist, which keeps its prior
//     context across calls.
//   - new_agent=true: Fresh=true requests a brand-new, clean-slate specialist.
//
// Continuity now lives in the reused agent's own conversation history, so the
// tool no longer prepends a textual replay of previous exchanges.
func TestOrchestratorDelegateTool_DefaultReusesAndNewAgent(t *testing.T) {
	var lastOpts orchestrator.AcquireOptions
	var prompts []string

	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m"},
			"reviewer":     {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 4},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "hub"},
	}
	pool := orchestrator.NewBoundedAgentPool(cfg, func(role, model string, opts orchestrator.AcquireOptions) (*orchestrator.AgentHandle, error) {
		lastOpts = opts
		h := orchestrator.NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error {
			if role == "reviewer" {
				prompts = append(prompts, prompt)
			}
			return nil
		}
		return h, nil
	})
	rt, err := orchestrator.NewRuntime(cfg, pool, nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	tool := &OrchestratorDelegateTool{Runtime: rt, Roles: []string{"reviewer"}}

	// Default: verbatim task, reuse (Fresh=false). The hub now delegates
	// asynchronously, so the tool returns immediately and the specialist runs in
	// the background; WaitForDelegations lets us observe the result.
	if _, err := tool.ExecuteContext(context.Background(), `{"role":"reviewer","task":"review index.html"}`); err != nil {
		t.Fatalf("default delegate: %v", err)
	}
	rt.WaitForDelegations()
	if len(prompts) != 1 || prompts[0] != "review index.html" {
		t.Fatalf("default should pass the task verbatim (no textual replay), got %v", prompts)
	}
	if lastOpts.Fresh {
		t.Fatalf("default should reuse the agent (Fresh=false), got Fresh=true")
	}

	// new_agent=true: a fresh, clean-slate specialist (Fresh=true).
	if _, err := tool.ExecuteContext(context.Background(), `{"role":"reviewer","task":"start over","new_agent":true}`); err != nil {
		t.Fatalf("new_agent delegate: %v", err)
	}
	rt.WaitForDelegations()
	if !lastOpts.Fresh {
		t.Fatalf("new_agent=true should request a fresh agent (Fresh=true)")
	}
	if len(prompts) != 2 || prompts[1] != "start over" {
		t.Fatalf("new_agent task should pass verbatim, got %v", prompts)
	}
}

// TestRuntimeAgentFactory_ReusesAgentWithContent verifies the harness default:
// sequential delegations to the same role reuse ONE pooled agent and that agent
// keeps its accumulated conversation content (it is not Clear()ed on reuse).
// It also verifies new_agent (Fresh) creates a distinct agent and that the idle
// pool keeps at most one agent per role so the agent set stays minimal.
func TestRuntimeAgentFactory_ReusesAgentWithContent(t *testing.T) {
	pool := multiagent.NewAgentPool(provider.Model{}, provider.StreamOptions{}, nil)
	adapter := NewOrchestratorAdapter(pool, nil, "")
	oCfg := config.OrchestratorConfig{Roles: map[string]config.OrchestratorRole{"coder": {Model: "m"}}}
	f := newRuntimeAgentFactory(adapter, oCfg, nil)
	agentCfg := multiagent.AgentConfig{}

	// First acquire: idle pool empty → a fresh agent.
	a1, err := f.acquire("coder", agentCfg, false)
	if err != nil {
		t.Fatalf("first acquire: %v", err)
	}
	const marker = "MARKER-FROM-FIRST-TASK"
	a1.InjectSystemMessage(marker)
	f.release("coder", a1, agentCfg)

	// Default acquire reuses the SAME agent and retains its content — the
	// "same agent, with content" default (regression guard: Clear() must NOT
	// be called on reuse).
	a2, err := f.acquire("coder", agentCfg, false)
	if err != nil {
		t.Fatalf("second acquire: %v", err)
	}
	if a2 != a1 {
		t.Fatalf("default acquire should reuse the pooled agent")
	}
	if !historyContains(a2.GetHistory(), marker) {
		t.Fatalf("reused agent must retain its prior content (was Clear()ed)")
	}

	// Fresh acquire creates a brand-new agent with no prior context.
	a3, err := f.acquire("coder", agentCfg, true)
	if err != nil {
		t.Fatalf("fresh acquire: %v", err)
	}
	if a3 == a1 {
		t.Fatalf("fresh=true should create a new agent, not reuse the pooled one")
	}
	if historyContains(a3.GetHistory(), marker) {
		t.Fatalf("fresh agent must not carry prior content")
	}

	// Idle pool keeps at most one agent per role (minimal agents).
	f.release("coder", a2, agentCfg)
	f.release("coder", a3, agentCfg)
	f.mu.Lock()
	got := len(f.pool["coder"])
	f.mu.Unlock()
	if got != 1 {
		t.Fatalf("idle pool should keep 1 agent per role, got %d", got)
	}
}

func historyContains(history []agentic.Message, needle string) bool {
	for _, m := range history {
		if strings.Contains(m.Content, needle) {
			return true
		}
	}
	return false
}
