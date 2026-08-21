// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"

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

	// ids mints per-role delegation ids (`dlg-<role>-<NN>`) so the review's
	// stream is attributable to its own per-delegation view (T4). Shared with
	// DelegateTool's minter shape; concurrency-safe.
	ids delegationIDMinter
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

// Deferred reports that request_review's schema is withheld from the eager
// block and loaded on demand via tool_search (P1). It is an opt-in-heavy
// companion tool; core tools stay eager.
func (*RequestReviewTool) Deferred() bool { return true }

// delegationContext returns the orchestrator's cancellation context when
// available, else a background context.
func delegationContext(orch *ForegroundOrchestrator) context.Context {
	if orch != nil {
		return orch.Context()
	}
	return context.Background()
}

// emitDelegationState nil-safely reports a delegation lifecycle transition so
// tools stay correct when the orchestrator is not wired yet.
func emitDelegationState(orch *ForegroundOrchestrator, role, delegationID, state, errMsg string) {
	if orch != nil {
		orch.EmitDelegationState(role, delegationID, state, errMsg)
	}
}

// beginDelegation binds delegationID to role for the run's duration and marks
// the delegation running. The returned func clears the binding (defer it).
func beginDelegation(orch *ForegroundOrchestrator, role, delegationID string) func() {
	if orch == nil {
		return func() {}
	}
	orch.SetActiveDelegation(role, delegationID)
	orch.EmitDelegationState(role, delegationID, DelegationRunning, "")
	return func() { orch.ClearActiveDelegation(role, delegationID) }
}

func (t *RequestReviewTool) Execute(input string) (string, error) {
	if !t.Enabled {
		return "", fmt.Errorf("agent-driven workflows are disabled. Enable with /companion:on, or use framework-driven companion mode with /companion:framework")
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

	// Mint a stable id for THIS review and bind it to the companion role for
	// the run's duration, so the review's streamed messages carry it and land
	// in their own per-delegation transcript (T4 — same seam as delegate_to).
	delegationID := t.ids.mint(gorole.Companion)
	defer beginDelegation(t.Orchestrator, gorole.Companion, delegationID)()

	ctx := delegationContext(t.Orchestrator)
	if err := companion.Run(ctx, params.Content); err != nil {
		emitDelegationState(t.Orchestrator, gorole.Companion, delegationID, DelegationFailed, err.Error())
		return "", fmt.Errorf("companion run: %w", err)
	}

	review := collectAgentOutput(t.Pool, gorole.Companion)
	if review == "" {
		emitDelegationState(t.Orchestrator, gorole.Companion, delegationID, DelegationCompleted, "")
		return fmt.Sprintf(`{"status":"review_complete","id":"%s","message":"no review output"}`, delegationID), nil
	}

	if err := sendToMain(t.Pool, review); err != nil {
		emitDelegationState(t.Orchestrator, gorole.Companion, delegationID, DelegationFailed, err.Error())
		return "", fmt.Errorf("send review to main: %w", err)
	}
	emitDelegationState(t.Orchestrator, gorole.Companion, delegationID, DelegationCompleted, "")
	return fmt.Sprintf(`{"status":"review_complete","id":"%s"}`, delegationID), nil
}

// DelegateTool allows the main agent to delegate a task to a sub-agent.
type DelegateTool struct {
	agentic.BaseTool
	Orchestrator *ForegroundOrchestrator
	Pool         *AgentPool
	Enabled      bool // set by AgentManager; when false, calls are rejected

	// ids mints per-role delegation ids (`dlg-<role>-<NN>`). Keyed by role so
	// two concurrent delegations to DIFFERENT roles never collide, and two to
	// the SAME role get 01/02/… in call order. Concurrency-safe.
	ids delegationIDMinter
}

// delegationIDMinter allocates `dlg-<role>-<NN>` ids. The per-role counter is
// created lazily and incremented atomically, so ids are unique across
// concurrent calls without a global lock. Shared by DelegateTool and
// RequestReviewTool (T4) so a delegate_to and a request_review for the same
// role never collide either.
type delegationIDMinter struct {
	seq sync.Map // role string → *atomic.Int64
}

// mint returns the next unique `dlg-<role>-<NN>` id for a role.
func (m *delegationIDMinter) mint(role string) string {
	v, _ := m.seq.LoadOrStore(role, &atomic.Int64{})
	n := v.(*atomic.Int64).Add(1)
	return fmt.Sprintf("dlg-%s-%02d", role, n)
}

// mintDelegationID returns the next unique `dlg-<role>-<NN>` id for a role.
// Kept as a thin wrapper over the shared minter (T0 tests exercise it
// directly).
func (t *DelegateTool) mintDelegationID(role string) string {
	return t.ids.mint(role)
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
		return "", fmt.Errorf("agent-driven workflows are disabled. Enable with /companion:on, or use framework-driven companion mode with /companion:framework")
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

	// Mint a stable id for THIS delegation; runDelegation binds it to the role
	// for the run's duration so streamed messages are attributable. Additive to
	// the ack JSON (`id` field); the full DelegationRegistry is out of scope.
	delegationID := t.mintDelegationID(params.Agent)
	if err := t.runDelegation(subAgent, params.Agent, params.Task, delegationID); err != nil {
		return "", err
	}
	return fmt.Sprintf(`{"status":"completed","agent":"%s","id":"%s"}`, params.Agent, delegationID), nil
}

// runDelegation executes one delegated sub-agent run under a minted delegation
// id: it binds the id to the role on the orchestrator (so emitKind stamps it
// onto the streamed OrchestratorMessages), brackets the run with delegation
// lifecycle markers (running → completed|failed — the T4 bug-2 fix so a
// delegation is visible from creation and a failure always leaves a terminal
// marker), runs the agent, and — for the companion role — forwards its output
// back to the main agent.
// runDelegation executes one delegated sub-agent run under a minted delegation
// id, bracketed by running→completed|failed lifecycle events. A per-delegation
// steering queue is bound for the duration of the run and attached to the
// sub-agent as its SteeringSource: user input typed on the delegation's tab
// (routed via ForegroundOrchestrator.SteerDelegation) is drained by the agent
// between stream rounds — the same mid-turn weaving the main agent gets.
func (t *DelegateTool) runDelegation(subAgent *agentic.Agent, role, task, delegationID string) error {
	defer beginDelegation(t.Orchestrator, role, delegationID)()

	// Bind mid-turn steering for this run; unbind + detach afterwards so a
	// reused pooled agent never keeps draining a dead delegation's queue.
	if t.Orchestrator != nil {
		if q := t.Orchestrator.BindDelegationSteering(delegationID); q != nil {
			subAgent.SetSteeringSource(q)
			defer func() {
				subAgent.SetSteeringSource(nil)
				t.Orchestrator.UnbindDelegationSteering(delegationID)
			}()
		}
	}

	ctx := delegationContext(t.Orchestrator)
	if err := subAgent.Run(ctx, task); err != nil {
		emitDelegationState(t.Orchestrator, role, delegationID, DelegationFailed, err.Error())
		return fmt.Errorf("%s execution failed: %w", role, err)
	}
	emitDelegationState(t.Orchestrator, role, delegationID, DelegationCompleted, "")

	if role == gorole.Companion {
		output := collectAgentOutput(t.Pool, role)
		if output != "" {
			if err := sendToMain(t.Pool, output); err != nil {
				return fmt.Errorf("send %s output to main: %w", role, err)
			}
		}
	}
	return nil
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
