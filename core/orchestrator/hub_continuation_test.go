// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
)

// newHubRuntime builds a hub runtime with orchestrator + coder roles backed by
// the given factory (store-less: events are not persisted).
func newHubRuntime(t *testing.T, factory AgentFactory) *Runtime {
	t.Helper()
	oCfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m"},
			"coder":        {Model: "m"},
			"reviewer":     {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 4},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "hub"},
	}
	rt, err := NewRuntime(oCfg, NewBoundedAgentPool(oCfg, factory), nil, "")
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	return rt
}

// TestBuildSpecialistResultsPrompt_SelfContained is the regression test for
// the hub continuation amnesia: continuation turns go to a FRESH orchestrator
// agent, so the prompt must restate the objective next to the specialist
// outputs. Before the fix it returned only "Specialist outputs:\n...".
func TestBuildSpecialistResultsPrompt_SelfContained(t *testing.T) {
	rt := newHubRuntime(t, nopFactory())
	rt.objective = "OBJ-123 write and review the file"
	rt.setLastMessage("coder", "WORK-DONE-456")

	p := rt.buildSpecialistResultsPrompt()
	for _, want := range []string{"OBJ-123", "WORK-DONE-456", "delegate"} {
		if !strings.Contains(p, want) {
			t.Errorf("continuation prompt missing %q\n--- prompt ---\n%s", want, p)
		}
	}
}

// TestManagedRoles_Sorted pins deterministic role ordering (pipeline stages
// must not depend on Go map iteration order).
func TestManagedRoles_Sorted(t *testing.T) {
	rt := newHubRuntime(t, nopFactory())
	got := rt.managedRoles()
	if len(got) != 2 || got[0] != "coder" || got[1] != "reviewer" {
		t.Errorf("managedRoles = %v, want [coder reviewer]", got)
	}
}

// TestHubLoop_ContinuationTurnCarriesObjective drives a full hub run where the
// orchestrator delegates on turn 1 and answers on turn 2. The second turn's
// prompt must contain the objective and the coder's output — otherwise the
// fresh orchestrator cannot know what remains to be done (e2e T1: reviewer
// delegation silently skipped).
func TestHubLoop_ContinuationTurnCarriesObjective(t *testing.T) {
	var mu sync.Mutex
	var orchPrompts []string

	var rt *Runtime
	factory := func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		switch role {
		case "orchestrator":
			h.Run = func(ctx context.Context, prompt string) error {
				mu.Lock()
				orchPrompts = append(orchPrompts, prompt)
				turn := len(orchPrompts)
				mu.Unlock()
				if turn == 1 {
					_, err := rt.DelegateAsync(ctx, "coder", "do the work", AcquireOptions{})
					return err
				}
				return nil // turn 2: final answer, no action
			}
		case "coder":
			h.Run = func(context.Context, string) error {
				h.AppendMessage("WORK-DONE-456")
				return nil
			}
		}
		return h, nil
	}
	rt = newHubRuntime(t, factory)

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := rt.Run(ctx, "OBJ-123 write then review"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	mu.Lock()
	defer mu.Unlock()
	if len(orchPrompts) != 2 {
		t.Fatalf("orchestrator turns = %d, want 2 (delegate, then final)", len(orchPrompts))
	}
	for _, want := range []string{"OBJ-123", "WORK-DONE-456", "delegate"} {
		if !strings.Contains(orchPrompts[1], want) {
			t.Errorf("turn-2 prompt missing %q\n--- prompt ---\n%s", want, orchPrompts[1])
		}
	}
}
