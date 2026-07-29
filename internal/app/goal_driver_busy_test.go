// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"errors"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/event"
)

// TestAgentManagerRunner_BusyReturnsErrAgentBusy is the app-level guard for
// bugs.md Issue 7 ("goal cannot be stopped"): while a user turn owns the
// agent, the goal driver's runner must refuse with core.ErrAgentBusy (clean
// stop, re-driven by the post-turn hook) instead of queueing a continuation
// prompt into the busy agent — the queue-on-busy + hot-loop was what flooded
// the agent with phantom "Continue working toward the active goal" turns.
func TestAgentManagerRunner_BusyReturnsErrAgentBusy(t *testing.T) {
	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")
	am.SetActiveAgentForTest(agentic.NewAgent(agentic.Config{}))
	am.SetRunningForTest(true)

	r := &agentManagerRunner{agentMgr: am}
	if err := r.Run(context.Background(), "continue"); !errors.Is(err, core.ErrAgentBusy) {
		t.Errorf("Run while busy = %v, want ErrAgentBusy", err)
	}
	if err := r.RunFresh(context.Background(), "continue", true); !errors.Is(err, core.ErrAgentBusy) {
		t.Errorf("RunFresh while busy = %v, want ErrAgentBusy", err)
	}
}

// TestAgentManagerRunner_IdleRunsAgainstCurrentAgent guards the normal path:
// with no user turn in flight the runner proceeds to the current agent (here
// nil → the runner must reach the "no active agent session" check, NOT the
// busy branch).
func TestAgentManagerRunner_IdleRunsAgainstCurrentAgent(t *testing.T) {
	am := core.NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")

	r := &agentManagerRunner{agentMgr: am}
	err := r.Run(context.Background(), "continue")
	if errors.Is(err, core.ErrAgentBusy) {
		t.Fatal("idle agent reported busy")
	}
	if err == nil || err.Error() != "no active agent session" {
		t.Errorf("Run with no session = %v, want no active agent session", err)
	}
}
