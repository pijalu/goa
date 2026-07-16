// SPDX-License-Identifier: GPL-3.0-or-later

package multiagent

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestAgentPool_CreateEphemeralAgent_IsolatedAndClean is the RED→GREEN test
// for the fresh-agent-per-delegation fix (R2/R3/R7/R8/R10/R12). Each
// orchestration delegation must get a distinct, isolated agent whose toolset
// is exactly its allow-listed base tools — never the workflow or companion
// extras that the cached GetOrCreate path injects.
func TestAgentPool_CreateEphemeralAgent_IsolatedAndClean(t *testing.T) {
	tools := []agentic.Tool{&agenticToolMock{name: "read"}, &agenticToolMock{name: "write"}}
	pool := NewAgentPool(testModel("m"), provider.StreamOptions{}, tools)
	// Configure the pool so the CACHED path (toolsForRole) would inject both
	// extras: a companion send_message tool (agent bus) and a workflow
	// workflows_next tool (foreground orchestrator). The ephemeral path must
	// exclude both.
	pool.SetAgentBus(agentic.NewAgentBus())
	pool.orch = &ForegroundOrchestrator{} // non-nil is all toolsForRole checks

	createdCalls := 0
	pool.OnAgentCreated = func(role string, agent *agentic.Agent) { createdCalls++ }

	cfg := AgentConfig{AllowedTools: []string{"read"}}
	a1, err := pool.CreateEphemeralAgent("coder", cfg)
	if err != nil {
		t.Fatalf("CreateEphemeralAgent: %v", err)
	}
	a2, err := pool.CreateEphemeralAgent("coder", cfg)
	if err != nil {
		t.Fatalf("CreateEphemeralAgent 2: %v", err)
	}

	// Each call must yield a distinct agent (isolated history/state).
	if a1 == a2 {
		t.Error("CreateEphemeralAgent returned the same agent twice; workers must be isolated")
	}

	// Ephemeral agents must NOT fire OnAgentCreated — that hook wires the
	// foreground orchestrator's companion observer, which is the root cause of
	// the "companion · cycle" leak during /orchestrate (R10).
	if createdCalls != 0 {
		t.Errorf("CreateEphemeralAgent fired OnAgentCreated %d times; ephemeral workers must not trigger companion wiring", createdCalls)
	}

	// Toolset: allow-listed base only. No send_message, no workflows_next.
	if names := toolNamesOf(a1); contains(names, "send_message") {
		t.Errorf("ephemeral agent leaked send_message tool: %v", names)
	} else if contains(names, "workflows_next") {
		t.Errorf("ephemeral agent leaked workflows_next tool: %v", names)
	} else if !contains(names, "read") || contains(names, "write") {
		t.Errorf("ephemeral agent tools = %v, want only [read] (allow-list)", names)
	}

	// Not cached: the cached path for the same role must build a different agent.
	cached, err := pool.GetOrCreate("coder")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if cached == a1 || cached == a2 {
		t.Error("CreateEphemeralAgent agent was cached; ephemeral agents must not pollute the role cache")
	}
}

func toolNamesOf(a *agentic.Agent) []string {
	var names []string
	for _, t := range a.Tools() {
		names = append(names, t.Schema().Name)
	}
	return names
}

func contains(haystack []string, needle string) bool {
	for _, s := range haystack {
		if s == needle {
			return true
		}
	}
	return false
}
