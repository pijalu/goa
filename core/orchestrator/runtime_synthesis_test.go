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

// synthesisFixture holds the mutable state shared by the fake agents in the
// synthesis regression tests.
type synthesisFixture struct {
	coderRuns     atomic.Int32
	reviewerRuns  atomic.Int32
	orchTurns     atomic.Int32
	synthPrompt   string
	rtRef         *Runtime
}

// makeOrchestratorRun returns a fake orchestrator Run that delegates to the
// configured specialists and records the synthesis prompt.
func (f *synthesisFixture) makeOrchestratorRun(h *AgentHandle) func(context.Context, string) error {
	return func(ctx context.Context, prompt string) error {
		if strings.Contains(prompt, "Specialist outputs:") {
			f.synthPrompt = prompt
			f.rtRef.RecordAgentMessage(h, "synthesis: all done")
			return nil
		}
		f.orchTurns.Add(1)
		if _, err := f.rtRef.Delegate(ctx, "coder", "write code"); err != nil {
			return err
		}
		if _, err := f.rtRef.Delegate(ctx, "reviewer", "review code"); err != nil {
			return err
		}
		return nil
	}
}

// makeSpecialistRun returns a fake specialist Run that records the given answer
// and increments the appropriate counter.
func (f *synthesisFixture) makeSpecialistRun(h *AgentHandle, counter *atomic.Int32, answer string) func(context.Context, string) error {
	return func(ctx context.Context, prompt string) error {
		counter.Add(1)
		f.rtRef.RecordAgentMessage(h, answer)
		return nil
	}
}

// newSynthesisRuntime builds a hub runtime with fake orchestrator, coder, and
// reviewer agents whose counters are exposed through the returned fixture.
func newSynthesisRuntime(t *testing.T) (*Runtime, *synthesisFixture) {
	t.Helper()
	fix := &synthesisFixture{}
	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m"},
			"coder":        {Model: "m"},
			"reviewer":     {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 8},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "hub"},
	}
	pool := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		switch role {
		case "orchestrator":
			h.Run = fix.makeOrchestratorRun(h)
		case "coder":
			h.Run = fix.makeSpecialistRun(h, &fix.coderRuns, "coder output: implemented feature X")
		case "reviewer":
			h.Run = fix.makeSpecialistRun(h, &fix.reviewerRuns, "reviewer output: found 2 issues")
		}
		return h, nil
	})
	rt, err := NewRuntime(cfg, pool, nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	fix.rtRef = rt
	rt.SetIDGenerator(func() string { return "hub-synth" })
	return rt, fix
}

// collectSynthesisEvents runs the runtime and returns all events emitted on its bus.
func collectSynthesisEvents(t *testing.T, rt *Runtime) []Event {
	t.Helper()
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
	return got
}

// assertPromptContains fails if prompt does not contain want.
func assertPromptContains(t *testing.T, prompt, want string) {
	t.Helper()
	if !strings.Contains(prompt, want) {
		t.Errorf("synthesis prompt missing %q:\n%s", want, prompt)
	}
}

// assertSynthesisMessageSeen reports whether an EventAgentMessage from the
// orchestrator with the given text was emitted.
func assertSynthesisMessageSeen(t *testing.T, got []Event, want string) {
	t.Helper()
	for _, ev := range got {
		if ev.Type == EventAgentMessage && ev.Role == "orchestrator" && strings.Contains(textOf(ev), want) {
			return
		}
	}
	t.Errorf("no synthesis EventAgentMessage for orchestrator with %q; events: %v", want, eventTypes(got))
}

// countOrchestratorStarts returns the number of EventAgentStarted events for
// the orchestrator role.
func countOrchestratorStarts(got []Event) int {
	n := 0
	for _, ev := range got {
		if ev.Type == EventAgentStarted && ev.Role == "orchestrator" {
			n++
		}
	}
	return n
}

// TestRuntime_HubSynthesizesSpecialistOutputs verifies Bug 5: after the
// orchestrator delegates to specialists, a second synthesis turn runs whose
// prompt inlines every specialist's output, and a synthesis EventAgentMessage
// is emitted. The delegate tool result already surfaces within the first turn;
// this is the robustness guarantee for models that stop without summarizing.
func TestRuntime_HubSynthesizesSpecialistOutputs(t *testing.T) {
	rt, fix := newSynthesisRuntime(t)
	got := collectSynthesisEvents(t, rt)

	if fix.coderRuns.Load() != 1 || fix.reviewerRuns.Load() != 1 {
		t.Errorf("specialist runs = coder %d reviewer %d, want 1/1", fix.coderRuns.Load(), fix.reviewerRuns.Load())
	}
	if fix.orchTurns.Load() != 1 {
		t.Errorf("orchestrator delegation turns = %d, want 1", fix.orchTurns.Load())
	}

	if fix.synthPrompt == "" {
		t.Fatal("synthesis turn never ran (no synthesis prompt captured)")
	}
	assertPromptContains(t, fix.synthPrompt, "coder output: implemented feature X")
	assertPromptContains(t, fix.synthPrompt, "reviewer output: found 2 issues")
	assertPromptContains(t, fix.synthPrompt, "build feature X")

	if got := countOrchestratorStarts(got); got != 2 {
		t.Errorf("orchestrator EventAgentStarted = %d, want 2 (delegation + synthesis)", got)
	}
	assertSynthesisMessageSeen(t, got, "synthesis: all done")
}

// fanoutFixture holds the mutable state for the fanout no-synthesis test.
type fanoutFixture struct {
	coderRuns     atomic.Int32
	reviewerRuns  atomic.Int32
	orchTurns     atomic.Int32
	synthPrompt   string
	rtRef         *Runtime
}

// makeFanoutAgentRun returns a fake Run for a fanout agent. The orchestrator
// role is tracked but must not produce output.
func (f *fanoutFixture) makeFanoutAgentRun(h *AgentHandle, role string) func(context.Context, string) error {
	return func(ctx context.Context, prompt string) error {
		if strings.Contains(prompt, "Specialist outputs:") {
			f.synthPrompt = prompt
			return nil
		}
		switch role {
		case "coder":
			f.coderRuns.Add(1)
			f.rtRef.RecordAgentMessage(h, "coder output")
		case "reviewer":
			f.reviewerRuns.Add(1)
			f.rtRef.RecordAgentMessage(h, "reviewer output")
		case "orchestrator":
			f.orchTurns.Add(1)
		}
		return nil
	}
}

// newFanoutRuntime builds a fanout runtime with fake agents.
func newFanoutRuntime(t *testing.T) (*Runtime, *fanoutFixture) {
	t.Helper()
	fix := &fanoutFixture{}
	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "m"},
			"coder":        {Model: "m"},
			"reviewer":     {Model: "m"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 8},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "fanout"},
	}
	pool := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		h := NewAgentHandle("", role, model)
		h.Run = fix.makeFanoutAgentRun(h, role)
		return h, nil
	})
	rt, err := NewRuntime(cfg, pool, nil, t.TempDir())
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	fix.rtRef = rt
	rt.SetIDGenerator(func() string { return "fanout-nosynth" })
	return rt, fix
}

// drainSynthesisEvents consumes the runtime event bus until it closes.
func drainSynthesisEvents(t *testing.T, rt *Runtime) {
	t.Helper()
	done := make(chan struct{})
	go func() { defer close(done); _ = rt.Run(context.Background(), "do thing") }()
	for range rt.Events() {
	}
	<-done
}

// TestRuntime_FanoutDoesNotSynthesize verifies the synthesis turn is hub-only:
// fanout does not run the orchestrator role at all, so no synthesis prompt is
// produced.
func TestRuntime_FanoutDoesNotSynthesize(t *testing.T) {
	rt, fix := newFanoutRuntime(t)
	drainSynthesisEvents(t, rt)

	if fix.synthPrompt != "" {
		t.Errorf("fanout must not run a synthesis turn; got prompt:\n%s", fix.synthPrompt)
	}
	if fix.orchTurns.Load() != 0 {
		t.Errorf("fanout must not run the orchestrator role; delegations = %d", fix.orchTurns.Load())
	}
}

// textOf reads the "text" payload field of an event.
func textOf(ev Event) string {
	if v, ok := ev.Payload["text"].(string); ok {
		return v
	}
	return ""
}
