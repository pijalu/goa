// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/telemetry"
	"github.com/pijalu/goa/multiagent"
)

// OrchestratorAdapter builds an orchestrator.Runtime whose AgentFactory
// bridges to a real multiagent.AgentPool. It is the production wiring between
// the pure core/orchestrator package and the agentic stack.
//
// Each role is resolved to a live *agentic.Agent via the multiagent pool
// (which handles model isolation, tool wiring, and prompt registry). The
// agent's output observer is translated into orchestrator AgentStats updates
// and AgentMessage events, so the runtime and TUI see uniform progress.
type OrchestratorAdapter struct {
	pool *multiagent.AgentPool
	cfg  *config.Config
	tel  orchestrator.Telemetry
}

// NewOrchestratorAdapter constructs an adapter over an existing multiagent pool.
func NewOrchestratorAdapter(pool *multiagent.AgentPool, cfg *config.Config) *OrchestratorAdapter {
	return &OrchestratorAdapter{
		pool: pool,
		cfg:  cfg,
	}
}

// SetTelemetry attaches a lifecycle tracker to every Runtime this adapter builds.
func (a *OrchestratorAdapter) SetTelemetry(t orchestrator.Telemetry) { a.tel = t }

// NewRuntime builds a fully-wired orchestrator.Runtime from the orchestrator
// config section. The event store is rooted at rootDir (typically ".goa/orchestrator").
func (a *OrchestratorAdapter) NewRuntime(oCfg config.OrchestratorConfig, rootDir string) (*orchestrator.Runtime, error) {
	var rt *orchestrator.Runtime

	factory := func(role, model string) (*orchestrator.AgentHandle, error) {
		rcfg := oCfg.Roles[role]
		maCfg := multiagent.AgentConfig{
			ModelName:    model,
			ProviderID:   rcfg.Provider,
			AllowedTools: rcfg.AllowedTools,
		}
		// Fresh, isolated agent per Acquire (i.e. per delegation). Each worker
		// gets its own conversation history, tool instances, and an observer
		// bound to its own handle, so the orchestrator can delegate to a series
		// of workers — including repeated or concurrent delegations to the
		// SAME role — without shared-state leakage (R2/R3/R7/R12) and without
		// inheriting the foreground orchestrator's companion observer, which
		// the cached GetOrCreate path attached via OnAgentCreated (R10).
		agent, err := a.pool.CreateEphemeralAgent(role, maCfg)
		if err != nil {
			return nil, err
		}

		h := orchestrator.NewAgentHandle("", role, model)
		h.Provider = resolveRoleProvider(rcfg, a.cfg)
		h.Thinking = string(agent.ReasoningEffort())
		h.Run = func(ctx context.Context, prompt string) error {
			return agent.Run(ctx, prompt)
		}

		// Only the orchestrator role drives specialists; workers receive just
		// their allow-listed base tools (no delegate, no workflow tool).
		if role == "orchestrator" {
			cur := append([]agentic.Tool{}, agent.Tools()...)
			cur = append(cur, &OrchestratorDelegateTool{Runtime: rt, Roles: delegateRoles(oCfg)})
			agent.SetTools(cur)
		}

		// Attach the orchestrator observer to THIS fresh agent, bound to THIS
		// handle. No dedupe is needed: every agent is distinct and dies with
		// its handle when released, so there is no cross-run accumulation.
		agent.AddObserver(agentic.OutputObserverFunc(func(ev agentic.OutputEvent) {
			applyOutputEvent(h, rt, ev)
		}))
		return h, nil
	}

	bounded := orchestrator.NewBoundedAgentPool(oCfg, factory)
	runID := internal.PrefixedHexID("run", 4)
	store := orchestrator.NewFileEventStore(rootDir, runID)
	var err error
	rt, err = orchestrator.NewRuntime(oCfg, bounded, store, rootDir)
	if err != nil {
		return nil, err
	}
	rt.SetIDGenerator(func() string { return runID })
	if a.tel != nil {
		rt.SetTelemetry(a.tel)
	}
	return rt, nil
}

// liveStatsInterval bounds how often in-flight token stats reach the TUI
// during a streaming turn. Tuned for visible live progress without flooding.
const liveStatsInterval = 200 * time.Millisecond

// applyOutputEvent translates an agentic.OutputEvent into AgentStats updates
// and runtime events. It is safe to call from the agent's observer goroutine.
func applyOutputEvent(h *orchestrator.AgentHandle, rt *orchestrator.Runtime, ev agentic.OutputEvent) {
	if h == nil {
		return
	}
	switch ev.Type {
	case agentic.EventToolCall:
		applyToolCall(h, rt, ev)
	case agentic.EventToolResult:
		applyToolResult(h, rt, ev)
	case agentic.EventTokenStats:
		applyTokenStats(h, rt, ev)
	case agentic.EventContent:
		applyContent(h, rt, ev)
	}
}

func applyToolCall(h *orchestrator.AgentHandle, rt *orchestrator.Runtime, ev agentic.OutputEvent) {
	h.Stats.IncToolCall()
	if rt != nil {
		rt.RecordAgentToolCall(h, ev.ToolName, ev.ToolInput, ev.ToolCallID)
	}
}

func applyToolResult(h *orchestrator.AgentHandle, rt *orchestrator.Runtime, ev agentic.OutputEvent) {
	if rt != nil {
		rt.RecordAgentToolResult(h, ev.ToolCallID, ev.Text, !isErrorResult(ev.Text))
	}
}

func applyTokenStats(h *orchestrator.AgentHandle, rt *orchestrator.Runtime, ev agentic.OutputEvent) {
	if ev.Timings != nil {
		h.Stats.AddUsage(ev.Timings.PromptN, ev.Timings.PredictedN,
			ev.Timings.CacheReadTokens, ev.Timings.CacheWriteTokens)
	}
	// Push a throttled live stats event so the TUI table updates in real
	// time during long turns (not just at turn end).
	if rt != nil {
		rt.EmitLiveStats(h, liveStatsInterval)
	}
}

func applyContent(h *orchestrator.AgentHandle, rt *orchestrator.Runtime, ev agentic.OutputEvent) {
	if ev.Role != agentic.Assistant || ev.Text == "" || rt == nil {
		return
	}
	switch ev.State {
	case agentic.StateThinking:
		rt.RecordAgentThinking(h, ev.Text)
	case agentic.StateContent:
		rt.RecordAgentMessage(h, ev.Text)
	}
}

// isErrorResult reports whether a tool result text should be treated as failed.
func isErrorResult(s string) bool {
	trimmed := strings.TrimSpace(s)
	return strings.HasPrefix(trimmed, "Error:") || strings.HasPrefix(trimmed, agentic.ToolBudgetResultPrefix)
}

// OrchestratorDelegateTool is the tool the orchestrator agent uses to delegate
// a sub-task to a specialist role. It calls Runtime.Delegate, which acquires a
// bounded-pool slot, runs one turn, and returns the sub-agent's answer.
//
// It is distinct from multiagent.DelegateTool (the foreground-orchestrator
// delegator) and lives here so core/orchestrator stays free of agentic imports.
//
// The tool keeps a short per-role conversation history so the orchestrator can
// continue a specialist conversation across multiple delegate calls instead of
// fire-and-forgetting each one. Each new call to the same role receives the
// previous task/response pairs as context, capped to avoid flooding the model.
type OrchestratorDelegateTool struct {
	agentic.BaseTool
	Runtime *orchestrator.Runtime
	// Roles enumerates the roles the orchestrator may delegate to (excludes
	// "orchestrator" itself), populated from config so the schema enum is exact.
	Roles []string

	// roleHistory remembers the last few exchanges for follow-up conversations.
	// It is safe without locking because the orchestrator agent runs one turn
	// at a time and delegate calls are sequential within that turn.
	roleHistory map[string][]delegateExchange
}

// delegateExchange records one side of an orchestrator/specialist
// conversation.
type delegateExchange struct {
	Task     string
	Response string
}

func (t *OrchestratorDelegateTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "delegate",
		Description: "Delegate a sub-task to a specialist agent role and return its answer. Call this tool multiple times for the same role to continue a conversation or provide missing context.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"role": map[string]any{
					"type":        "string",
					"description": "The role to delegate to",
					"enum":        t.Roles,
				},
				"task": map[string]any{
					"type":        "string",
					"description": "The concrete task for the sub-agent",
				},
			},
			"required": []string{"role", "task"},
		},
	}
}

func (t *OrchestratorDelegateTool) Execute(input string) (string, error) {
	return t.ExecuteContext(context.Background(), input)
}

func (t *OrchestratorDelegateTool) ExecuteContext(ctx context.Context, input string) (string, error) {
	var params struct {
		Role    string `json:"role"`
		Task    string `json:"task"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}
	if params.Role == "" || params.Task == "" {
		return "", fmt.Errorf("role and task are required")
	}
	if t.Runtime == nil {
		return "", fmt.Errorf("orchestrator runtime unavailable")
	}

	// Continue the conversation: prepend the previous exchanges for this role
	// so the new specialist instance has the context it needs for follow-up.
	fullTask := t.priorExchanges(params.Role) + params.Task

	out, err := t.Runtime.Delegate(ctx, params.Role, fullTask)
	if err != nil {
		return "", err
	}
	if out == "" {
		out = fmt.Sprintf("[%s] completed the task (no text output).", params.Role)
	}
	t.recordExchange(params.Role, params.Task, out)
	return out, nil
}

// priorExchanges returns the formatted conversation history for a role, or an
// empty string if there is none. The result is always empty for the first call.
func (t *OrchestratorDelegateTool) priorExchanges(role string) string {
	if t.roleHistory == nil {
		return ""
	}
	var b strings.Builder
	for _, ex := range t.roleHistory[role] {
		b.WriteString("[previous task]\n")
		b.WriteString(ex.Task)
		b.WriteString("\n\n[previous response]\n")
		b.WriteString(ex.Response)
		b.WriteString("\n\n---\n\n")
	}
	return b.String()
}

// recordExchange appends a task/response pair to the per-role history,
// keeping only the most recent entries so the context window stays bounded.
func (t *OrchestratorDelegateTool) recordExchange(role, task, response string) {
	if t.roleHistory == nil {
		t.roleHistory = map[string][]delegateExchange{}
	}
	h := t.roleHistory[role]
	const maxHistory = 3
	if len(h) >= maxHistory {
		h = h[len(h)-maxHistory+1:]
	}
	const maxResponse = 2000
	r := response
	if len(r) > maxResponse {
		r = r[:maxResponse] + "\n... [truncated]"
	}
	h = append(h, delegateExchange{Task: task, Response: r})
	t.roleHistory[role] = h
}

// delegateRoles returns the roles the orchestrator may delegate to (everything
// except "orchestrator"), used to populate the DelegateTool schema enum.
func delegateRoles(oCfg config.OrchestratorConfig) []string {
	var roles []string
	for name := range oCfg.Roles {
		if name == "orchestrator" {
			continue
		}
		roles = append(roles, name)
	}
	return roles
}

// resolveRoleProvider returns the provider id for an orchestrator role. It
// prefers the role's explicit provider binding and falls back to the
// process-wide active provider so every row of the stats table is non-empty.
func resolveRoleProvider(rcfg config.OrchestratorRole, cfg *config.Config) string {
	if rcfg.Provider != "" {
		return rcfg.Provider
	}
	if cfg != nil && cfg.ActiveProvider != "" {
		return cfg.ActiveProvider
	}
	return ""
}

// telClientAdapter adapts *telemetry.Client (Record(name, map[string]string))
// to the orchestrator.Telemetry interface (Track(event, map[string]any)).
type telClientAdapter struct {
	client *telemetry.Client
}

// Track converts the props to string metadata and forwards to Record.
func (t *telClientAdapter) Track(event string, props map[string]any) {
	if t == nil || t.client == nil {
		return
	}
	meta := make(map[string]string, len(props))
	for k, v := range props {
		meta[k] = fmt.Sprintf("%v", v)
	}
	t.client.Record(event, meta)
}
