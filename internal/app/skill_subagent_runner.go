// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"fmt"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/multiagent"
)

// skillSubAgentRunner executes a skill body in an isolated sub-agent created
// from the application's agent pool. It implements core.SkillSubAgentRunner.
type skillSubAgentRunner struct {
	pool *multiagent.AgentPool
}

// Run creates a one-off task agent with the given system prompt, runs the task,
// and returns the collected assistant output. allowedTools restricts the tools
// available to the sub-agent; if empty, a safe default set is used. run_skill
// and terminal are always excluded.
func (r *skillSubAgentRunner) Run(ctx context.Context, systemPrompt, task string, allowedTools []string) (string, error) {
	if r.pool == nil {
		return "", fmt.Errorf("agent pool not available")
	}
	role := fmt.Sprintf("skill-%d", time.Now().UnixNano())
	toolNames := r.resolveToolNames(allowedTools)
	cfg := multiagent.AgentConfig{
		SystemPrompt:    systemPrompt,
		AllowedTools:    toolNames,
		ReasoningEffort: agentic.ReasoningEffortOff,
	}
	agent, err := r.pool.CreateTaskAgent(role, cfg)
	if err != nil {
		return "", fmt.Errorf("create skill sub-agent: %w", err)
	}
	defer r.pool.Evict(role)
	return agent.RunAndCollect(ctx, task)
}

// resolveToolNames returns the tool names to expose to a sub-agent.
// If allowed is empty, a safe default set is used. run_skill and terminal
// are always excluded because the sub-agent should not recurse into skills or
// use the restricted-environment terminal.
func (r *skillSubAgentRunner) resolveToolNames(allowed []string) []string {
	all := r.pool.ToolNames()
	if len(allowed) == 0 {
		allowed = defaultSubAgentTools(all)
	}
	return filterToolNames(allowed, "run_skill", "terminal")
}

// defaultSubAgentTools returns a safe default set of tools from the parent.
func defaultSubAgentTools(all []string) []string {
	candidates := map[string]bool{
		"bash":     true,
		"read":     true,
		"edit":     true,
		"write":    true,
		"webfetch": true,
	}
	var out []string
	for _, name := range all {
		if candidates[name] {
			out = append(out, name)
		}
	}
	return out
}

// filterToolNames returns all names except those in excluded.
func filterToolNames(names []string, excluded ...string) []string {
	ex := make(map[string]bool, len(excluded))
	for _, e := range excluded {
		ex[e] = true
	}
	var out []string
	for _, n := range names {
		if !ex[n] {
			out = append(out, n)
		}
	}
	return out
}

// Ensure skillSubAgentRunner implements core.SkillSubAgentRunner.
var _ core.SkillSubAgentRunner = (*skillSubAgentRunner)(nil)
