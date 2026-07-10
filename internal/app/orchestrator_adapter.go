// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"
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

	fac := newRuntimeAgentFactory(a, oCfg, &rt)
	bounded := orchestrator.NewBoundedAgentPool(oCfg, fac.build)
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

// runtimeAgentFactory is the per-runtime state behind the AgentFactory passed
// to the bounded agent pool. It owns a small pool of idle specialist agents so
// sequential delegations to the same role can reuse an agent instead of
// creating a new one each time.
type runtimeAgentFactory struct {
	adapter *OrchestratorAdapter
	oCfg    config.OrchestratorConfig
	rt      **orchestrator.Runtime
	pool    map[string][]pooledAgent
	mu      sync.Mutex
}

type pooledAgent struct {
	agent *agentic.Agent
	cfg   multiagent.AgentConfig
}

func newRuntimeAgentFactory(adapter *OrchestratorAdapter, oCfg config.OrchestratorConfig, rt **orchestrator.Runtime) *runtimeAgentFactory {
	return &runtimeAgentFactory{adapter: adapter, oCfg: oCfg, rt: rt, pool: map[string][]pooledAgent{}}
}

func (f *runtimeAgentFactory) build(role, model string, opts orchestrator.AcquireOptions) (*orchestrator.AgentHandle, error) {
	maCfg := f.agentConfig(role, model)
	agent, err := f.acquire(role, maCfg, opts.Fresh)
	if err != nil {
		return nil, err
	}

	h := f.newHandle(role, model, agent, maCfg)
	removeObs := agent.AddObserver(agentic.OutputObserverFunc(func(ev agentic.OutputEvent) {
		applyOutputEvent(h, *f.rt, ev)
	}))
	go func() {
		<-h.Done()
		removeObs()
		f.release(role, agent, maCfg)
	}()
	return h, nil
}

func (f *runtimeAgentFactory) agentConfig(role, model string) multiagent.AgentConfig {
	rcfg := f.oCfg.Roles[role]
	return multiagent.AgentConfig{
		ModelName:    model,
		ProviderID:   rcfg.Provider,
		AllowedTools: rcfg.AllowedTools,
	}
}

// acquire returns the pooled agent for the role — reused WITH its accumulated
// conversation content — or creates a fresh one. The default (fresh=false)
// keeps specialist agents minimal and context-preserving: sequential
// delegations to the same role continue the same conversation. fresh=true
// forces a brand-new agent with no prior context, honoring the delegate
// tool's explicit "new agent" choice.
func (f *runtimeAgentFactory) acquire(role string, maCfg multiagent.AgentConfig, fresh bool) (*agentic.Agent, error) {
	if role != "orchestrator" && !fresh {
		f.mu.Lock()
		if pool := f.pool[role]; len(pool) > 0 {
			pa := pool[0]
			f.pool[role] = pool[1:]
			f.mu.Unlock()
			return pa.agent, nil
		}
		f.mu.Unlock()
	}
	return f.adapter.pool.CreateEphemeralAgent(role, maCfg)
}

// release returns a non-orchestrator agent to the idle pool, keeping at most
// ONE agent per role so the agent set stays minimal. The kept agent retains
// its conversation content for reuse by the next default delegation; a
// freshly-created agent replaces any previously-pooled one so the most
// recently used specialist is the one retained.

func (f *runtimeAgentFactory) release(role string, agent *agentic.Agent, maCfg multiagent.AgentConfig) {
	if role == "orchestrator" {
		return
	}
	pa := pooledAgent{agent: agent, cfg: maCfg}
	f.mu.Lock()
	if len(f.pool[role]) == 0 {
		f.pool[role] = append(f.pool[role], pa)
	} else {
		f.pool[role][0] = pa
	}
	f.mu.Unlock()
}

// newHandle creates a populated AgentHandle and, for the orchestrator role,
// injects the delegate tool.
func (f *runtimeAgentFactory) newHandle(role, model string, agent *agentic.Agent, maCfg multiagent.AgentConfig) *orchestrator.AgentHandle {
	rcfg := f.oCfg.Roles[role]
	h := orchestrator.NewAgentHandle("", role, model)
	h.Provider = resolveRoleProvider(rcfg, f.adapter.cfg)
	h.Thinking = string(agent.ReasoningEffort())
	h.Run = func(ctx context.Context, prompt string) error {
		return agent.Run(ctx, prompt)
	}

	if role == "orchestrator" {
		cur := append([]agentic.Tool{}, agent.Tools()...)
		cur = append(cur, &OrchestratorDelegateTool{Runtime: *f.rt, Roles: delegateRoles(f.oCfg)})
		agent.SetTools(cur)
	}
	return h
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
	case agentic.EventContextStats:
		applyContextStats(h, rt, ev)
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

func applyContextStats(h *orchestrator.AgentHandle, rt *orchestrator.Runtime, ev agentic.OutputEvent) {
	if ev.ContextStats == nil {
		return
	}
	h.Stats.SetContext(ev.ContextStats.EstimatedTokens, ev.ContextStats.MaxTokens, ev.ContextStats.AutoMax)
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

// isErrorResult reports whether a tool result text should be treated as failed
// or as a synthetic guardrail message. Guardrail messages (budget exceeded,
// duplicate call hints, loop guardrails) are not real tool results and are
// rendered as warnings rather than successful results.
func isErrorResult(s string) bool {
	trimmed := strings.TrimSpace(s)
	return strings.HasPrefix(trimmed, "Error:") || agentic.IsGuardrailResult(s)
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
}

func (t *OrchestratorDelegateTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "delegate",
		Description: "Delegate a sub-task to a specialist agent role and return its answer. By default the same agent is reused across calls to a role so it keeps the prior context; set new_agent=true to start a fresh, clean-slate specialist for this task.",
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
				"new_agent": map[string]any{
					"type":        "boolean",
					"description": "If true, use a brand-new agent with no prior context for this task (default false: reuse the role's agent and its accumulated context).",
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
		Role     string `json:"role"`
		Task     string `json:"task"`
		NewAgent bool   `json:"new_agent"`
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

	// Default: reuse the role's pooled agent, which retains its conversation
	// context across delegations. new_agent=true requests a fresh, clean-slate
	// specialist instead. The agent's own history is the single source of
	// truth for continuity, so no textual replay is prepended.
	out, err := t.Runtime.DelegateWith(ctx, params.Role, params.Task, orchestrator.AcquireOptions{Fresh: params.NewAgent})
	if err != nil {
		return "", err
	}
	if out == "" {
		out = fmt.Sprintf("[%s] completed the task (no text output).", params.Role)
	}
	return out, nil
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
