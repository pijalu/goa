// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"sync"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/multiagent"
)

// CompanionCoordinator manages the companion agent reference and triggers
// framework-driven companion review after a main-agent turn completes.
type CompanionCoordinator struct {
	mu             sync.Mutex
	companionAgent *agentic.Agent
	foregroundOrch *multiagent.ForegroundOrchestrator
}

// NewCompanionCoordinator creates a companion coordinator.
func NewCompanionCoordinator() *CompanionCoordinator {
	return &CompanionCoordinator{}
}

// SetForegroundOrchestrator sets the orchestrator used for companion workflows.
func (cc *CompanionCoordinator) SetForegroundOrchestrator(orch *multiagent.ForegroundOrchestrator) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.foregroundOrch = orch
}

// SetCompanionAgent stores the companion agent and registers it on the bus.
func (cc *CompanionCoordinator) SetCompanionAgent(agent *agentic.Agent, bus *agentic.AgentBus) {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	cc.companionAgent = agent
	if bus != nil {
		bus.Unregister("companion")
		_, _ = bus.Register("companion")
	}
}

// Agent returns the stored companion agent, if any.
func (cc *CompanionCoordinator) Agent() *agentic.Agent {
	cc.mu.Lock()
	defer cc.mu.Unlock()
	return cc.companionAgent
}

// RunPostTurn runs the companion review in the background when framework-driven
// companion mode is active and there is main output to review.
func (cc *CompanionCoordinator) RunPostTurn(mainOutput string, emitFlash func(string)) {
	cc.mu.Lock()
	orch := cc.foregroundOrch
	cc.mu.Unlock()

	if orch == nil {
		return
	}
	if orch.Mode() != multiagent.WorkflowCompanionMinor {
		return
	}
	if mainOutput == "" {
		if emitFlash != nil {
			emitFlash("Companion: no main output to review")
		}
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
		defer cancel()
		if err := orch.AfterMainTurn(ctx, mainOutput); err != nil {
			if emitFlash != nil {
				emitFlash("Companion error: " + err.Error())
			}
		}
	}()
}
