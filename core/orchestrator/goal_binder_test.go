// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"sync"
	"testing"

	"github.com/pijalu/goa/config"
)

// fakeGoalBinder is a recording GoalBinder for runtime goal-binding tests.
type fakeGoalBinder struct {
	mu        sync.Mutex
	tokens    int
	overAt    int  // tokens at which RecordTokens reports over-budget
	over      bool
	completed bool
	blocked   bool
	completeReason string
	blockReason    string
	created   string
}

func (f *fakeGoalBinder) Create(objective string, tokenBudget int) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.overAt = tokenBudget
	f.created = objective
	return "goal-1", nil
}

func (f *fakeGoalBinder) RecordTokens(delta int) (bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.tokens += delta
	if f.overAt > 0 && f.tokens >= f.overAt {
		f.over = true
	}
	return f.over, nil
}

func (f *fakeGoalBinder) Complete(reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.completed = true
	f.completeReason = reason
	return nil
}

func (f *fakeGoalBinder) Block(reason string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.blocked = true
	f.blockReason = reason
	return nil
}

func goalTestCfg(topology string) config.OrchestratorConfig {
	return config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"coder":    {Model: "m"},
			"reviewer": {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 4},
		Defaults: config.OrchestratorDefaultsConfig{Topology: topology},
	}
}

func TestRuntime_GoalBinding_CompletesOnSuccess(t *testing.T) {
	cfg := goalTestCfg("fanout")
	pool := NewBoundedAgentPool(cfg, func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error {
			h.Stats.AddUsage(20, 10, 0, 0) // 30 tokens
			return nil
		}
		return h, nil
	})
	rt, _ := NewRuntime(cfg, pool, nil, t.TempDir())
	gb := &fakeGoalBinder{}
	rt.SetGoalBinder(gb)

	if err := rt.Run(context.Background(), "obj"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if gb.tokens != 60 { // two agents × 30 tokens
		t.Errorf("accrued tokens = %d, want 60", gb.tokens)
	}
	if !gb.completed {
		t.Errorf("goal not marked complete on successful run")
	}
	if gb.blocked {
		t.Errorf("goal should not be blocked on success")
	}
}

func TestRuntime_GoalBinding_BudgetExhaustionBlocks(t *testing.T) {
	cfg := goalTestCfg("pipeline") // sequential so budget bites predictably
	pool := NewBoundedAgentPool(cfg, func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error {
			h.Stats.AddUsage(40, 10, 0, 0) // 50 tokens each
			return nil
		}
		return h, nil
	})
	rt, _ := NewRuntime(cfg, pool, nil, t.TempDir())
	gb := &fakeGoalBinder{overAt: 50} // budget exhausted after first agent
	rt.SetGoalBinder(gb)

	err := rt.Run(context.Background(), "obj")
	if err == nil {
		t.Fatalf("expected budget-exhaustion error")
	}
	if !gb.over {
		t.Errorf("expected over-budget signal")
	}
	// Finalization must mark the goal blocked (run failed).
	if !gb.blocked {
		t.Errorf("goal should be blocked after budget exhaustion")
	}
	if gb.completed {
		t.Errorf("goal should NOT be completed after budget exhaustion")
	}
}

func TestRuntime_GoalBinding_NoopWhenUnbound(t *testing.T) {
	cfg := goalTestCfg("fanout")
	pool := NewBoundedAgentPool(cfg, func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error { return nil }
		return h, nil
	})
	rt, _ := NewRuntime(cfg, pool, nil, t.TempDir())
	if rt.GoalBound() {
		t.Error("GoalBound should be false without a binder")
	}
	if err := rt.Run(context.Background(), "obj"); err != nil {
		t.Fatalf("Run: %v", err)
	}
}

// recordingTelemetry captures Track calls.
type recordingTelemetry struct {
	events []string
}

func (r *recordingTelemetry) Track(event string, _ map[string]any) {
	r.events = append(r.events, event)
}

func TestRuntime_TelemetryEmitted(t *testing.T) {
	cfg := goalTestCfg("fanout")
	pool := NewBoundedAgentPool(cfg, func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error { return nil }
		return h, nil
	})
	rt, _ := NewRuntime(cfg, pool, nil, t.TempDir())
	tel := &recordingTelemetry{}
	rt.SetTelemetry(tel)
	if err := rt.Run(context.Background(), "obj"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	want := []string{TelemetryRunStarted, TelemetryRunFinished}
	if len(tel.events) != len(want) {
		t.Fatalf("telemetry events = %v, want %v", tel.events, want)
	}
	for i, w := range want {
		if tel.events[i] != w {
			t.Errorf("event[%d] = %q, want %q", i, tel.events[i], w)
		}
	}
}

func TestRuntime_SetTelemetryNilIsSafe(t *testing.T) {
	cfg := goalTestCfg("fanout")
	pool := NewBoundedAgentPool(cfg, func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error { return nil }
		return h, nil
	})
	rt, _ := NewRuntime(cfg, pool, nil, t.TempDir())
	rt.SetTelemetry(nil) // must not panic
	if err := rt.Run(context.Background(), "obj"); err != nil {
		t.Fatalf("Run: %v", err)
	}
}
