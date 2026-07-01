// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pijalu/goa/internal/agentic"
	gorole "github.com/pijalu/goa/internal/role"
)

// RequestReviewTool allows the main agent to request a code review
// from the companion sub-agent via the AgentPool and AgentBus.
type RequestReviewTool struct {
	agentic.BaseTool
	Pool         *AgentPool
	Orchestrator *ForegroundOrchestrator
	Enabled      bool // set by AgentManager; when false, calls are rejected
}

func (t *RequestReviewTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "request_review",
		Description: "Request a code review from the companion agent.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"content": map[string]any{
					"type":        "string",
					"description": "The code or implementation to review",
				},
			},
			"required": []string{"content"},
		},
	}
}

func (t *RequestReviewTool) Execute(input string) (string, error) {
	if !t.Enabled {
		return "", fmt.Errorf("agent-driven workflows are disabled. Enable with /agent-driven:on, or use framework-driven workflows with /workflows:run:review")
	}
	var params struct {
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.Content == "" {
		return "", fmt.Errorf("content is required")
	}
	if t.Pool == nil {
		return "", fmt.Errorf("agent pool not configured")
	}

	companion, err := t.Pool.GetOrCreate(gorole.Companion)
	if err != nil {
		return "", fmt.Errorf("create companion: %w", err)
	}

	ctx := context.Background()
	if t.Orchestrator != nil {
		ctx = t.Orchestrator.Context()
	}
	if err := companion.Run(ctx, params.Content); err != nil {
		return "", fmt.Errorf("companion run: %w", err)
	}

	review := collectAgentOutput(t.Pool, gorole.Companion)
	if review == "" {
		return `{"status":"review_complete","message":"no review output"}`, nil
	}

	if err := sendToMain(t.Pool, review); err != nil {
		return "", fmt.Errorf("send review to main: %w", err)
	}
	return `{"status":"review_complete"}`, nil
}

// DelegateTool allows the main agent to delegate a task to a sub-agent.
type DelegateTool struct {
	agentic.BaseTool
	Orchestrator *ForegroundOrchestrator
	Pool         *AgentPool
	Enabled      bool // set by AgentManager; when false, calls are rejected
}

func (t *DelegateTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "delegate_to",
		Description: "Delegate a task to a specific sub-agent (coder, companion, planner).",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"agent": map[string]any{
					"type":        "string",
					"description": "The role of the agent to delegate to",
					"enum":        []string{gorole.Coder, gorole.Companion, gorole.Planner},
				},
				"task": map[string]any{
					"type":        "string",
					"description": "The task description for the sub-agent",
				},
			},
			"required": []string{"agent", "task"},
		},
	}
}

func (t *DelegateTool) Execute(input string) (string, error) {
	if !t.Enabled {
		return "", fmt.Errorf("agent-driven workflows are disabled. Enable with /agent-driven:on, or use framework-driven workflows with /workflows:run:pair")
	}
	var params struct {
		Agent string `json:"agent"`
		Task  string `json:"task"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	if params.Agent == "" {
		return "", fmt.Errorf("agent is required")
	}
	if params.Task == "" {
		return "", fmt.Errorf("task is required")
	}
	if t.Pool == nil {
		return "", fmt.Errorf("agent pool not configured")
	}

	subAgent, err := t.Pool.GetOrCreate(params.Agent)
	if err != nil {
		return "", fmt.Errorf("create sub-agent %q: %w", params.Agent, err)
	}

	ctx := context.Background()
	if t.Orchestrator != nil {
		ctx = t.Orchestrator.Context()
	}
	if err := subAgent.Run(ctx, params.Task); err != nil {
		return "", fmt.Errorf("%s execution failed: %w", params.Agent, err)
	}

	if params.Agent == gorole.Companion {
		output := collectAgentOutput(t.Pool, params.Agent)
		if output != "" {
			if err := sendToMain(t.Pool, output); err != nil {
				return "", fmt.Errorf("send %s output to main: %w", params.Agent, err)
			}
		}
	}
	return fmt.Sprintf(`{"status":"completed","agent":"%s"}`, params.Agent), nil
}

func collectAgentOutput(pool *AgentPool, role string) string {
	agent := pool.Get(role)
	if agent == nil {
		return ""
	}
	history := agent.GetHistory()
	if len(history) == 0 {
		return ""
	}
	for i := len(history) - 1; i >= 0; i-- {
		msg := history[i]
		if msg.Role == agentic.Assistant && msg.Content != "" {
			return msg.Content
		}
	}
	return ""
}

func sendToMain(pool *AgentPool, content string) error {
	if pool.agentBus == nil {
		return fmt.Errorf("agent bus not configured")
	}
	return pool.agentBus.Send(context.Background(), agentic.CommMessage{
		From:    gorole.Companion,
		To:      gorole.Main,
		Content: fmt.Sprintf("Message from companion:\n```\n%s\n```", content),
	})
}

// AgentDrivenTools returns the agent-driven tool set.
//
// Agent-Driven architecture: the main LLM agent decides when to invoke
// multi-agent workflows by calling these tools as part of its reasoning.
// Contrast with Framework-Driven where the user explicitly triggers
// workflows via slash commands.
//
// Tools:
//   - request_review — agent asks the companion sub-agent to critique output
//   - delegate_to    — agent hands a task to coder/companion/planner
//
// These tools are registered on the main agent's tool list so the LLM can
// call them during a turn. When the LLM emits a tool call, the tool executes
// the corresponding workflow through the ForegroundOrchestrator.
//
// The user controls whether agent-driven workflows are active via:
//
//	/agent-driven:on  — allow the LLM to call these tools
//	/agent-driven:off — reject tool calls with a helpful message
//
// Safety: if orchestrator/pool are nil, tools return descriptive errors
// instead of panicking, so they can be registered early and wired later.
func AgentDrivenTools(orch *ForegroundOrchestrator, pool *AgentPool) []agentic.Tool {
	return []agentic.Tool{
		&RequestReviewTool{Pool: pool},
		&DelegateTool{Orchestrator: orch, Pool: pool},
	}
}
