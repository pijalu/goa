// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"fmt"
	"sync"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/internal/agentic"
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
	pool  *multiagent.AgentPool
	cfg   *config.Config
	mu    sync.Mutex
	seen  map[string]struct{} // roles already wired this process (observer dedupe)
}

// NewOrchestratorAdapter constructs an adapter over an existing multiagent pool.
func NewOrchestratorAdapter(pool *multiagent.AgentPool, cfg *config.Config) *OrchestratorAdapter {
	return &OrchestratorAdapter{
		pool: pool,
		cfg:  cfg,
		seen: map[string]struct{}{},
	}
}

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
		h.Run = func(ctx context.Context, prompt string) error {
			return agent.Run(ctx, prompt)
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
	runID := fmt.Sprintf("run-%d", time.Now().UnixNano())
	store := orchestrator.NewFileEventStore(rootDir, runID)
	var err error
	rt, err = orchestrator.NewRuntime(oCfg, bounded, store, rootDir)
	if err != nil {
		return nil, err
	}
	rt.SetIDGenerator(func() string { return runID })
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
