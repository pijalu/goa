// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/pijalu/goa/config"
)

// makeOrchestratorDelegateRun returns a fake Run that delegates the given task
// to the given role and asserts the result contains wantSubstring. The runtime
// and handle are captured when the closure is created.
func makeOrchestratorDelegateRun(t *testing.T, rtRef **Runtime, h *AgentHandle, role, task, wantSubstring string) func(context.Context, string) error {
	return func(ctx context.Context, prompt string) error {
		if strings.Contains(prompt, "Specialist outputs:") {
			(*rtRef).RecordAgentMessage(h, "synthesis: "+wantSubstring)
			return nil
		}
		out, err := (*rtRef).Delegate(ctx, role, task)
		if err != nil {
			return err
		}
		if !strings.Contains(out, wantSubstring) {
			t.Errorf("delegate returned %q, want it to contain %q", out, wantSubstring)
		}
		return nil
	}
}

// makeCoderAnswerRun returns a fake Run that records the given answer and
// counts executions.
func makeCoderAnswerRun(rtRef **Runtime, h *AgentHandle, counter *atomic.Int32, answer string) func(context.Context, string) error {
	return func(ctx context.Context, prompt string) error {
		counter.Add(1)
		(*rtRef).RecordAgentMessage(h, answer)
		return nil
	}
}

// makeDelegateRoundTripPool builds a pool whose orchestrator delegates to the
// coder role and whose coder returns a fixed answer.
func makeDelegateRoundTripPool(t *testing.T, rtRef **Runtime, coderRuns *atomic.Int32) (*BoundedAgentPool, config.OrchestratorConfig) {
	t.Helper()
	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m"},
			"coder":        {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 4},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "hub"},
	}
	pool := NewBoundedAgentPool(cfg, func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		switch role {
		case "orchestrator":
			h.Run = makeOrchestratorDelegateRun(t, rtRef, h, "coder", "compute answer", "42")
		case "coder":
			h.Run = makeCoderAnswerRun(rtRef, h, coderRuns, "the answer is 42")
		}
		return h, nil
	})
	return pool, cfg
}

// TestRuntime_DelegateRoundTrip proves the hub primitive: the orchestrator
// role is driven, and when its (fake) turn "delegates" by calling Delegate
// directly, a specialist agent is acquired, run, released, and its streamed
// text is returned as the delegation result.
func TestRuntime_DelegateRoundTrip(t *testing.T) {
	var coderRuns atomic.Int32
	var rtRef *Runtime

	pool, cfg := makeDelegateRoundTripPool(t, &rtRef, &coderRuns)
	rt, err := NewRuntime(cfg, pool, nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	rtRef = rt
	rt.SetIDGenerator(func() string { return "hub-test" })
	if err := rt.Run(context.Background(), "plan and delegate"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if got := coderRuns.Load(); got != 1 {
		t.Errorf("coder ran %d times, want 1", got)
	}
}

// TestRuntime_DelegateUnknownRole errors cleanly.
func TestRuntime_DelegateUnknownRole(t *testing.T) {
	cfg := config.OrchestratorConfig{
		Roles:    map[string]config.OrchestratorRole{"orchestrator": {Model: "m"}},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 4},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "hub"},
	}
	pool := NewBoundedAgentPool(cfg, func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error { return nil }
		return h, nil
	})
	rt, _ := NewRuntime(cfg, pool, nil, t.TempDir())
	if _, err := rt.Delegate(context.Background(), "ghost", "x"); err == nil {
		t.Errorf("expected error delegating to unknown role")
	}
}
