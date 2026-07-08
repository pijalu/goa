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
)

// TestOrchestratorDelegateTool_ContinuesConversation proves the harness keeps a
// short per-role conversation history so follow-up delegate calls carry the
// previous task/response context. This is the RED regression for the
// "conversation not happening" bug in bugs.md: specialists and orchestrator
// should be able to have back-and-forth instead of fire-and-forget.
func TestOrchestratorDelegateTool_ContinuesConversation(t *testing.T) {
	var prompts []string

	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m"},
			"reviewer":     {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 4},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "hub"},
	}
	pool := orchestrator.NewBoundedAgentPool(cfg, func(role, model string) (*orchestrator.AgentHandle, error) {
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

	// First delegate: the reviewer asks for clarification.
	_, _ = tool.ExecuteContext(context.Background(), `{"role":"reviewer","task":"review index.html"}`)

	// Second delegate: the harness must have remembered the previous exchange
	// and prepended it to the new task so the fresh reviewer instance has
	// context for the follow-up.
	_, _ = tool.ExecuteContext(context.Background(), `{"role":"reviewer","task":"please read the file with your read tool"}`)

	if len(prompts) < 2 {
		t.Fatalf("expected 2 reviewer prompts, got %d", len(prompts))
	}
	second := prompts[1]
	if !strings.Contains(second, "[previous task]") {
		t.Fatalf("second prompt missing previous task context; got:\n%s", second)
	}
	if !strings.Contains(second, "review index.html") {
		t.Fatalf("second prompt missing original task; got:\n%s", second)
	}
	if !strings.Contains(second, "[previous response]") {
		t.Fatalf("second prompt missing previous response context; got:\n%s", second)
	}
}
