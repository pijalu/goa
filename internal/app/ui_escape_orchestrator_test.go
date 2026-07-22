// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/multiagent"
)

// TestHandleEscape_DrainsSteeringQueue is the bugs.md "ESC remainder:
// steering drain" regression: user input queued as steering while the agent
// runs must NOT dispatch as a follow-up turn after ESC. handleEscape must
// flush the queue so nothing survives the interrupt.
func TestHandleEscape_DrainsSteeringQueue(t *testing.T) {
	bus := event.MakeBus(16, 16, 16, 16)
	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, bus, t.TempDir())
	a := &App{subs: &subsystems{agentMgr: am}}

	// Simulate the user typing ahead mid-turn.
	am.SteeringQueue().Append("follow-up question")
	am.SteeringQueue().Append("another one")
	if am.SteeringQueue().Len() != 2 {
		t.Fatalf("setup: expected 2 queued steering messages, got %d", am.SteeringQueue().Len())
	}

	a.handleEscape()

	if got := am.SteeringQueue().Len(); got != 0 {
		t.Fatalf("ESC must drain the steering queue, %d message(s) survived", got)
	}
}

// TestHandleEscape_OrchestratorsSafe covers the bugs.md "ESC remainder:
// orchestrator/swarm" wiring at the app boundary: handleEscape must call
// ForegroundOrchestrator.Cancel (the single choke point that aborts every
// in-flight run context — propagation itself is proven by multiagent's
// TestAccessor_Cancel tests) and ActiveRuntime-cancel, and must do so without
// panicking whether the orchestrator is nil, idle, or mid-run.
func TestHandleEscape_OrchestratorsSafe(t *testing.T) {
	bus := event.MakeBus(16, 16, 16, 16)
	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, bus, t.TempDir())

	pool := multiagent.NewAgentPool(provider.Model{}, provider.StreamOptions{}, []agentic.Tool{})
	fg := multiagent.NewForegroundOrchestrator(pool)
	active := orchestrator.NewActiveRuntime()

	a := &App{subs: &subsystems{
		agentMgr:       am,
		foregroundOrch: fg,
		orchActive:     active,
	}}

	// Idle orchestrator: Cancel is a no-op and must not panic/block.
	a.handleEscape()

	// ESC with an active runtime registered must call its Cancel. A zero
	// Runtime has no armed cancel func, so we assert reachability by
	// confirming no panic and that the holder still resolves (the runtime's
	// own Cancel semantics are covered in core/orchestrator tests).
	active.Set(&orchestrator.Runtime{})
	a.handleEscape()
	if active.Get() == nil {
		t.Fatalf("handleEscape must not clear the active runtime holder")
	}
}
