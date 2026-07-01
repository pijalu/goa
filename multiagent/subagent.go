// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"context"

	"github.com/pijalu/goa/internal/agentic"
)

// SubagentHost runs isolated sub-agents and returns their results.
type SubagentHost interface {
	// Run executes a sub-agent with the given profile and prompt.
	Run(ctx context.Context, profile string, prompt string) (string, error)
}

// poolHost adapts an AgentPool to the SubagentHost interface.
type poolHost struct {
	pool *AgentPool
}

// Run resolves the profile and runs a one-off task agent.
func (h *poolHost) Run(ctx context.Context, profile, prompt string) (string, error) {
	cfg := AgentConfig{}
	if h.pool != nil {
		cfg = h.pool.RoleConfig(profile)
	}
	agent, err := h.pool.CreateTaskAgent(profile+"-task", cfg)
	if err != nil {
		return "", err
	}
	return agent.RunAndCollect(ctx, prompt)
}

// NewSubagentHost wraps an AgentPool as a SubagentHost.
func NewSubagentHost(pool *AgentPool) SubagentHost {
	return &poolHost{pool: pool}
}

// AgentRunner is implemented by types that can run an agent and collect output.
type AgentRunner interface {
	RunAndCollect(ctx context.Context, input string) (string, error)
}

var _ AgentRunner = (*agentic.Agent)(nil)
var _ SubagentHost = (*poolHost)(nil)
