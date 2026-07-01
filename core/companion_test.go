// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/multiagent"
)

func TestCompanionCoordinator_SetAndGetAgent(t *testing.T) {
	cc := NewCompanionCoordinator()
	if cc.Agent() != nil {
		t.Error("expected nil agent initially")
	}

	agent := agentic.NewAgent(agentic.Config{SystemPrompt: "companion"})
	bus := agentic.NewAgentBus()
	cc.SetCompanionAgent(agent, bus)

	if cc.Agent() != agent {
		t.Error("Agent() did not return stored agent")
	}
}

func TestCompanionCoordinator_RunPostTurn_NoCompanionMode(t *testing.T) {
	cc := NewCompanionCoordinator()
	pool := multiagent.NewAgentPool(agenticprovider.Model{}, agenticprovider.StreamOptions{}, nil)
	orch := multiagent.NewForegroundOrchestrator(pool)
	cc.SetForegroundOrchestrator(orch)

	called := false
	cc.RunPostTurn("output", func(string) { called = true })
	time.Sleep(10 * time.Millisecond)
	if called {
		t.Error("flash should not be called when not in companion minor mode")
	}
}

func TestCompanionCoordinator_RunPostTurn_EmptyOutput(t *testing.T) {
	cc := NewCompanionCoordinator()
	pool := multiagent.NewAgentPool(agenticprovider.Model{}, agenticprovider.StreamOptions{}, nil)
	orch := multiagent.NewForegroundOrchestrator(pool)
	orch.SetMode(multiagent.WorkflowCompanionMinor)
	cc.SetForegroundOrchestrator(orch)

	var flash string
	cc.RunPostTurn("", func(s string) { flash = s })
	time.Sleep(10 * time.Millisecond)
	if flash == "" {
		t.Error("expected flash for empty output")
	}
}
