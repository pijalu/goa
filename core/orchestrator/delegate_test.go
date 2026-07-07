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

// TestRuntime_DelegateRoundTrip proves the hub primitive: the orchestrator
// role is driven, and when its (fake) turn "delegates" by calling Delegate
// directly, a specialist agent is acquired, run, released, and its streamed
// text is returned as the delegation result.
func TestRuntime_DelegateRoundTrip(t *testing.T) {
	var coderRuns atomic.Int32
	var rtRef *Runtime
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
			h.Run = func(ctx context.Context, prompt string) error {
				// The synthesis turn inlines specialist outputs and must not
				// re-delegate; detect it by its prompt and produce a summary.
				if strings.Contains(prompt, "Specialist outputs:") {
					rtRef.RecordAgentMessage(h, "synthesis: the answer is 42")
					return nil
				}
				// Simulate the orchestrator delegating via the tool path.
				out, err := rtRef.Delegate(ctx, "coder", "compute answer")
				if err != nil {
					return err
				}
				if !strings.Contains(out, "42") {
					t.Errorf("delegate returned %q, want it to contain 42", out)
				}
				return nil
			}
		case "coder":
			h.Run = func(ctx context.Context, prompt string) error {
				coderRuns.Add(1)
				rtRef.RecordAgentMessage(h, "the answer is 42")
				return nil
			}
		}
		return h, nil
	})
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
