// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/internal/agentic"
)

// TestOrchestratorAdapterEvents_ForwardContextStats asserts that an
// EventContextStats from the agent updates the handle's context counters and
// emits a live EventAgentStats so the TUI can show per-agent context usage.
func TestOrchestratorAdapterEvents_ForwardContextStats(t *testing.T) {
	var mu sync.Mutex
	var statsEvents []orchestrator.Event

	h := orchestrator.NewAgentHandle("h-1", "coder", "gemma")
	h.Provider = "google"

	rt := newTestRuntimeForAdapter(t, func(evt orchestrator.Event) {
		mu.Lock()
		defer mu.Unlock()
		if evt.Type == orchestrator.EventAgentStats {
			statsEvents = append(statsEvents, evt)
		}
	})

	observer := func(ev agentic.OutputEvent) { applyOutputEvent(h, rt, ev) }
	observer(agentic.OutputEvent{
		Type: agentic.EventContextStats,
		ContextStats: &agentic.ContextStats{
			EstimatedTokens: 1234,
			MaxTokens:       100000,
			AutoMax:         true,
		},
	})

	// Live stats are emitted asynchronously with a throttle; allow the goroutine to fire.
	time.Sleep(50 * time.Millisecond)

	snap := h.Stats.Snapshot()
	if snap.ContextEstimate != 1234 {
		t.Errorf("ContextEstimate = %d, want 1234", snap.ContextEstimate)
	}
	if snap.ContextMax != 100000 {
		t.Errorf("ContextMax = %d, want 100000", snap.ContextMax)
	}
	if !snap.ContextAutoMax {
		t.Error("ContextAutoMax = false, want true")
	}

	mu.Lock()
	got := append([]orchestrator.Event(nil), statsEvents...)
	mu.Unlock()
	if len(got) == 0 {
		t.Fatal("expected at least one EventAgentStats from context update")
	}
	last := got[len(got)-1].Payload
	if orchInt(last, "context_estimate") != 1234 {
		t.Errorf("stats payload context_estimate = %d, want 1234", orchInt(last, "context_estimate"))
	}
	if orchInt(last, "context_max") != 100000 {
		t.Errorf("stats payload context_max = %d, want 100000", orchInt(last, "context_max"))
	}
	if !orchBool(last, "context_auto_max") {
		t.Error("stats payload context_auto_max = false, want true")
	}
}

// TestOrchestratorAdapterEvents_ForwardThinkingAndToolEvents asserts that the
// adapter's observer emits EventAgentThinking, EventAgentToolCall and
// EventAgentToolResult from the runtime in addition to the existing
// EventAgentMessage and stats plumbing.
func TestOrchestratorAdapterEvents_ForwardThinkingAndToolEvents(t *testing.T) {
	var mu sync.Mutex
	var received []orchestrator.Event

	h := orchestrator.NewAgentHandle("h-1", "coder", "gemma")
	h.Provider = "google"

	rt := newTestRuntimeForAdapter(t, func(evt orchestrator.Event) {
		mu.Lock()
		received = append(received, evt)
		mu.Unlock()
	})

	observer := func(ev agentic.OutputEvent) { applyOutputEvent(h, rt, ev) }

	observer(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "reasoning "})
	observer(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "continues"})
	observer(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "answer "})
	observer(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "text"})
	observer(agentic.OutputEvent{Type: agentic.EventToolCall, Role: agentic.Assistant, ToolName: "writefile", ToolInput: `{"path":"x.txt"}`, ToolCallID: "t1"})
	observer(agentic.OutputEvent{Type: agentic.EventToolResult, Role: agentic.Assistant, ToolName: "writefile", ToolCallID: "t1", Text: "written"})

	// Wait for the async emit (bus is buffered, but emit is synchronous in our
	// fake runtime below). Allow a small window for the goroutine to collect.
	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	got := append([]orchestrator.Event(nil), received...)
	mu.Unlock()

	assertHas := func(want orchestrator.EventType) {
		t.Helper()
		for _, ev := range got {
			if ev.Type == want {
				return
			}
		}
		t.Errorf("missing event type %s in received events: %v", want, eventTypes(got))
	}
	assertHas(orchestrator.EventAgentThinking)
	assertHas(orchestrator.EventAgentMessage)
	assertHas(orchestrator.EventAgentToolCall)
	assertHas(orchestrator.EventAgentToolResult)

	// Check accumulated text on the handle (the per-delegation source of
	// truth). The old role-keyed MessageFor buffer is gone — two concurrent
	// delegate(coder) calls used to clobber it.
	if got := h.Message(); got != "answer text" {
		t.Errorf("handle.Message() = %q, want %q", got, "answer text")
	}
}

func findToolResultOK(got []orchestrator.Event, id string) *bool {
	for _, ev := range got {
		if ev.Type != orchestrator.EventAgentToolResult {
			continue
		}
		gotID, _ := ev.Payload["call_id"].(string)
		if gotID != id {
			continue
		}
		if b, ok := ev.Payload["ok"].(bool); ok {
			return &b
		}
	}
	return nil
}

// TestOrchestratorAdapterEvents_ToolResultErrorStatus asserts that the adapter
// marks tool results starting with Error: or any goa-system guardrail/budget
// prefix as not ok.
func TestOrchestratorAdapterEvents_ToolResultErrorStatus(t *testing.T) {
	var mu sync.Mutex
	var received []orchestrator.Event

	h := orchestrator.NewAgentHandle("h-1", "coder", "gemma")
	rt := newTestRuntimeForAdapter(t, func(evt orchestrator.Event) {
		mu.Lock()
		received = append(received, evt)
		mu.Unlock()
	})

	observer := func(ev agentic.OutputEvent) { applyOutputEvent(h, rt, ev) }
	observer(agentic.OutputEvent{Type: agentic.EventToolResult, ToolCallID: "t1", Text: "Error: file not found"})
	observer(agentic.OutputEvent{Type: agentic.EventToolResult, ToolCallID: "t2", Text: agentic.ToolBudgetResultPrefix})
	observer(agentic.OutputEvent{Type: agentic.EventToolResult, ToolCallID: "t3", Text: agentic.ToolRepeatedMessagePrefix})
	observer(agentic.OutputEvent{Type: agentic.EventToolResult, ToolCallID: "t4", Text: agentic.ToolLoopMessagePrefix})

	time.Sleep(10 * time.Millisecond)
	mu.Lock()
	got := append([]orchestrator.Event(nil), received...)
	mu.Unlock()

	for _, id := range []string{"t1", "t2", "t3", "t4"} {
		ok := findToolResultOK(got, id)
		if ok == nil || *ok {
			t.Errorf("tool result %s should be ok=false, got ok=%v", id, ok)
		}
	}
}

func newTestRuntimeForAdapter(t *testing.T, emitFn func(orchestrator.Event)) *orchestrator.Runtime {
	t.Helper()
	cfg := config.OrchestratorConfig{Roles: map[string]config.OrchestratorRole{"coder": {Model: "gemma"}}}
	pool := orchestrator.NewBoundedAgentPool(cfg, func(role, model string, _ orchestrator.AcquireOptions) (*orchestrator.AgentHandle, error) {
		return orchestrator.NewAgentHandle("fake-"+role, role, model), nil
	})
	rt, err := orchestrator.NewRuntime(cfg, pool, nil, "")
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}
	if emitFn != nil {
		// Replace the bus consumer with our own so emitted events are captured
		// synchronously. The real runtime is closed before use, so this is safe.
		ch := rt.Events()
		go func() {
			for ev := range ch {
				emitFn(ev)
			}
		}()
	}
	return rt
}

func eventTypes(evts []orchestrator.Event) []orchestrator.EventType {
	out := make([]orchestrator.EventType, len(evts))
	for i, ev := range evts {
		out[i] = ev.Type
	}
	return out
}

// allow context import if needed elsewhere.
var _ = context.Background()
