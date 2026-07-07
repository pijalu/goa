// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/pijalu/goa/config"
)

// synthesisFactory builds a hub runtime where the orchestrator delegates to a
// coder and a reviewer on its first turn, and (on the synthesis turn) records
// the prompt it received. Specialists record distinct outputs. It returns
// pointers to the counters/prompt for assertions.
func synthesisFactory(t *testing.T) (*Runtime, *atomic.Int32, *atomic.Int32, *atomic.Int32, *string) {
	t.Helper()
	var coderRuns, reviewerRuns, orchDelegations atomic.Int32
	var synthPrompt string
	var rtRef *Runtime
	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m"},
			"coder":        {Model: "m"},
			"reviewer":     {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 8},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "hub"},
	}
	pool := NewBoundedAgentPool(cfg, func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		switch role {
		case "orchestrator":
			h.Run = func(ctx context.Context, prompt string) error {
				if strings.Contains(prompt, "Specialist outputs:") {
					synthPrompt = prompt
					rtRef.RecordAgentMessage(h, "synthesis: all done")
					return nil
				}
				orchDelegations.Add(1)
				if _, err := rtRef.Delegate(ctx, "coder", "write code"); err != nil {
					return err
				}
				if _, err := rtRef.Delegate(ctx, "reviewer", "review code"); err != nil {
					return err
				}
				return nil
			}
		case "coder":
			h.Run = func(ctx context.Context, prompt string) error {
				coderRuns.Add(1)
				rtRef.RecordAgentMessage(h, "coder output: implemented feature X")
				return nil
			}
		case "reviewer":
			h.Run = func(ctx context.Context, prompt string) error {
				reviewerRuns.Add(1)
				rtRef.RecordAgentMessage(h, "reviewer output: found 2 issues")
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
	rt.SetIDGenerator(func() string { return "hub-synth" })
	return rt, &coderRuns, &reviewerRuns, &orchDelegations, &synthPrompt
}

// TestRuntime_HubSynthesizesSpecialistOutputs verifies Bug 5: after the
// orchestrator delegates to specialists, a second synthesis turn runs whose
// prompt inlines every specialist's output, and a synthesis EventAgentMessage
// is emitted. The delegate tool result already surfaces within the first turn;
// this is the robustness guarantee for models that stop without summarizing.
func TestRuntime_HubSynthesizesSpecialistOutputs(t *testing.T) {
	rt, coderRuns, reviewerRuns, orchDelegations, synthPrompt := synthesisFactory(t)

	var mu sync.Mutex
	var got []Event
	done := make(chan struct{})
	go func() { defer close(done); _ = rt.Run(context.Background(), "build feature X") }()
	for ev := range rt.Events() {
		mu.Lock()
		got = append(got, ev)
		mu.Unlock()
	}
	<-done

	if coderRuns.Load() != 1 || reviewerRuns.Load() != 1 {
		t.Errorf("specialist runs = coder %d reviewer %d, want 1/1", coderRuns.Load(), reviewerRuns.Load())
	}
	if orchDelegations.Load() != 1 {
		t.Errorf("orchestrator delegation turns = %d, want 1", orchDelegations.Load())
	}

	prompt := *synthPrompt
	if prompt == "" {
		t.Fatal("synthesis turn never ran (no synthesis prompt captured)")
	}
	if !strings.Contains(prompt, "coder output: implemented feature X") {
		t.Errorf("synthesis prompt missing coder output:\n%s", prompt)
	}
	if !strings.Contains(prompt, "reviewer output: found 2 issues") {
		t.Errorf("synthesis prompt missing reviewer output:\n%s", prompt)
	}
	if !strings.Contains(prompt, "build feature X") {
		t.Errorf("synthesis prompt missing objective:\n%s", prompt)
	}

	// A synthesis EventAgentMessage for the orchestrator must be emitted.
	var synthSeen bool
	var orchestratorStarts int
	for _, ev := range got {
		if ev.Type == EventAgentStarted && ev.Role == "orchestrator" {
			orchestratorStarts++
		}
		if ev.Type == EventAgentMessage && ev.Role == "orchestrator" &&
			strings.Contains(textOf(ev), "synthesis: all done") {
			synthSeen = true
		}
	}
	if orchestratorStarts != 2 {
		t.Errorf("orchestrator EventAgentStarted = %d, want 2 (delegation + synthesis)", orchestratorStarts)
	}
	if !synthSeen {
		t.Errorf("no synthesis EventAgentMessage for orchestrator; events: %v", eventTypes(got))
	}
}

// TestRuntime_FanoutDoesNotSynthesize verifies the synthesis turn is hub-only:
// fanout does not run the orchestrator role at all, so no synthesis prompt is
// produced.
func TestRuntime_FanoutDoesNotSynthesize(t *testing.T) {
	var coderRuns, reviewerRuns, orchDelegations atomic.Int32
	var synthPrompt string
	var rtRef *Runtime
	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m"},
			"coder":        {Model: "m"},
			"reviewer":     {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 8},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "fanout"},
	}
	pool := NewBoundedAgentPool(cfg, func(role, model string) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = func(ctx context.Context, prompt string) error {
			if strings.Contains(prompt, "Specialist outputs:") {
				synthPrompt = prompt
				return nil
			}
			switch role {
			case "coder":
				coderRuns.Add(1)
				rtRef.RecordAgentMessage(h, "coder output")
			case "reviewer":
				reviewerRuns.Add(1)
				rtRef.RecordAgentMessage(h, "reviewer output")
			case "orchestrator":
				orchDelegations.Add(1)
			}
			return nil
		}
		return h, nil
	})
	rt, err := NewRuntime(cfg, pool, nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	rtRef = rt
	rt.SetIDGenerator(func() string { return "fanout-nosynth" })

	done := make(chan struct{})
	go func() { defer close(done); _ = rt.Run(context.Background(), "do thing") }()
	for range rt.Events() {
	}
	<-done

	if synthPrompt != "" {
		t.Errorf("fanout must not run a synthesis turn; got prompt:\n%s", synthPrompt)
	}
	if orchDelegations.Load() != 0 {
		t.Errorf("fanout must not run the orchestrator role; delegations = %d", orchDelegations.Load())
	}
}

// textOf reads the "text" payload field of an event.
func textOf(ev Event) string {
	if v, ok := ev.Payload["text"].(string); ok {
		return v
	}
	return ""
}
