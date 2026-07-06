// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"sync"

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
	mu   sync.Mutex
	seen map[string]struct{} // roles already wired this process (observer dedupe)
	tel  orchestrator.Telemetry
}

// NewOrchestratorAdapter constructs an adapter over an existing multiagent pool.
func NewOrchestratorAdapter(pool *multiagent.AgentPool, cfg *config.Config) *OrchestratorAdapter {
	return &OrchestratorAdapter{
		pool: pool,
		cfg:  cfg,
		seen: map[string]struct{}{},
	}
}

// SetTelemetry attaches a lifecycle tracker to every Runtime this adapter builds.
func (a *OrchestratorAdapter) SetTelemetry(t orchestrator.Telemetry) { a.tel = t }

// NewRuntime builds a fully-wired orchestrator.Runtime from the orchestrator
// config section. The event store is rooted at rootDir (typically ".goa/orchestrator").
func (a *OrchestratorAdapter) NewRuntime(oCfg config.OrchestratorConfig, rootDir string) (*orchestrator.Runtime, error) {
	var rt *orchestrator.Runtime

	factory := func(role, model string) (*orchestrator.AgentHandle, error) {
		// Configure the role's model on the multiagent pool so GetOrCreate
		// builds the agent against the right provider/model. Use the passed
		// oCfg as the source of truth for role bindings.
		rcfg := oCfg.Roles[role]
		maCfg := multiagent.AgentConfig{
			ModelName:    model,
			ProviderID:   rcfg.Provider,
			AllowedTools: rcfg.AllowedTools,
		}
		a.pool.SetConfig(role, maCfg)
		agent, err := a.pool.GetOrCreate(role)
		if err != nil {
			return nil, err
		}

		h := orchestrator.NewAgentHandle("", role, model)
		h.Provider = resolveRoleProvider(rcfg, a.cfg)
		h.Thinking = string(agent.ReasoningEffort())
		h.Run = func(ctx context.Context, prompt string) error {
			return agent.Run(ctx, prompt)
		}

		// The orchestrator role gets the DelegateTool so it can drive
		// specialists itself (true hub topology). The tool closes over `rt`,
		// which is assigned below before any turn runs.
		if role == "orchestrator" {
			roles := delegateRoles(oCfg)
			cur := append([]agentic.Tool{}, agent.Tools()...)
			cur = append(cur, &OrchestratorDelegateTool{Runtime: rt, Roles: roles})
			agent.SetTools(cur)
		}

		// Attach the observer exactly once per (process, role) to avoid
		// accumulation across multiple runs sharing the cached agent.
		// (multiagent does not expose observer removal; for long-lived
		// processes, prefer CreateTaskAgent with a unique role per run.)
		a.mu.Lock()
		_, already := a.seen[role]
		if !already {
			a.seen[role] = struct{}{}
		}
		a.mu.Unlock()
		if !already {
			agent.AddObserver(agentic.OutputObserverFunc(func(ev agentic.OutputEvent) {
				applyOutputEvent(h, rt, ev)
			}))
		}
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

// applyOutputEvent translates an agentic.OutputEvent into AgentStats updates
// and AgentMessage events on the runtime. It is safe to call from the agent's
// observer goroutine.
func applyOutputEvent(h *orchestrator.AgentHandle, rt *orchestrator.Runtime, ev agentic.OutputEvent) {
	if h == nil {
		return
	}
	switch ev.Type {
	case agentic.EventToolCall:
		h.Stats.IncToolCall()
	case agentic.EventTokenStats:
		if ev.Timings != nil {
			h.Stats.AddUsage(ev.Timings.PromptN, ev.Timings.PredictedN,
				ev.Timings.CacheReadTokens, ev.Timings.CacheWriteTokens)
		}
	case agentic.EventContent:
		if ev.Role == agentic.Assistant && ev.State == agentic.StateContent && ev.Text != "" {
			if rt != nil {
				rt.RecordAgentMessage(h, ev.Text)
			}
		}
	}
}

// OrchestratorDelegateTool is the tool the orchestrator agent uses to delegate
// a sub-task to a specialist role. It calls Runtime.Delegate, which acquires a
// bounded-pool slot, runs one turn, and returns the sub-agent's answer.
//
// It is distinct from multiagent.DelegateTool (the foreground-orchestrator
// delegator) and lives here so core/orchestrator stays free of agentic imports.
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
		Description: "Delegate a sub-task to a specialist agent role and return its answer.",
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
		Role string `json:"role"`
		Task string `json:"task"`
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
	out, err := t.Runtime.Delegate(ctx, params.Role, params.Task)
	if err != nil {
		return "", err
	}
	if out == "" {
		return fmt.Sprintf("[%s] completed the task (no text output).", params.Role), nil
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
