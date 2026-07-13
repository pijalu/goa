// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"context"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
)

// collectRuntimeEvents drains the runtime event bus and returns all events it
// emitted until the bus closes.
func collectRuntimeEvents(t *testing.T, rt *Runtime) []Event {
	t.Helper()
	var got []Event
	ch := rt.Events()
	done := make(chan struct{})
	go func() {
		for ev := range ch {
			got = append(got, ev)
		}
		close(done)
	}()

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	rt.Cancel()
	_ = rt.Run(ctx, "noop")
	<-done
	return got
}

// assertHasEventType fails if no event in got has the wanted type.
func assertHasEventType(t *testing.T, got []Event, want EventType) {
	t.Helper()
	for _, ev := range got {
		if ev.Type == want {
			return
		}
	}
	t.Errorf("missing event type %s in %v", want, eventTypesFor(got))
}

// assertEventRoleAgent fails if the event does not carry the expected role and agent id.
func assertEventRoleAgent(t *testing.T, ev Event, role, agentID string) {
	t.Helper()
	if ev.Role != role || ev.AgentID != agentID {
		t.Errorf("event %s has wrong role/agent: got role=%q agent=%q, want role=%q agent=%q", ev.Type, ev.Role, ev.AgentID, role, agentID)
	}
}

// assertToolResultPayload checks the ok and text fields of a tool result event.
func assertToolResultPayload(t *testing.T, ev Event, wantText string, wantOK bool) {
	t.Helper()
	ok, okok := ev.Payload["ok"].(bool)
	if !okok || ok != wantOK {
		t.Errorf("EventAgentToolResult ok = %v (okok=%v), want %v", ok, okok, wantOK)
	}
	if text, _ := ev.Payload["text"].(string); text != wantText {
		t.Errorf("EventAgentToolResult text = %q, want %q", text, wantText)
	}
}

// TestRuntime_SteerAll_ResumesPausedLoop asserts that SteerAll resumes the
// orchestrator loop when it is paused waiting for a user answer. The default
// TUI steering target is "all", so a broadcast must not leave the
// orchestrator stuck in the paused state.
func TestRuntime_SteerAll_ResumesPausedLoop(t *testing.T) {
	cfg := testRuntimeConfig()
	pool := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		return NewAgentHandle("fake-"+role, role, model), nil
	})
	rt, err := NewRuntime(cfg, pool, nil, "")
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	resume := make(chan struct{})
	rt.loopMu.Lock()
	rt.loopActive = true
	rt.pendingUser = true
	rt.resumeCh = resume
	rt.loopMu.Unlock()

	rt.SteerAll("answer")

	select {
	case <-resume:
		// resumed as expected
	case <-time.After(2 * time.Second):
		t.Fatal("SteerAll did not resume a paused orchestrator loop")
	}
}

// TestRuntime_SteerAgent_BuffersWithoutResume verifies that SteerAgent
// appends steering to the targeted handle without resuming a paused
// orchestrator loop. Use SteerOrchestrator (or SteerAll) to resume the
// orchestrator specifically.
func TestRuntime_SteerAgent_BuffersWithoutResume(t *testing.T) {
	cfg := config.OrchestratorConfig{
		Roles: map[string]config.OrchestratorRole{
			"orchestrator": {Model: "gemma"},
			"coder":        {Model: "gemma"},
		},
		Pool:     config.OrchestratorPoolConfig{MaxTotalAgents: 2},
		Defaults: config.OrchestratorDefaultsConfig{Topology: "hub"},
	}
	pool := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		return NewAgentHandle("fake-"+role, role, model), nil
	})
	rt, err := NewRuntime(cfg, pool, nil, "")
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	resume := make(chan struct{})
	rt.loopMu.Lock()
	rt.loopActive = true
	rt.pendingUser = true
	rt.resumeCh = resume
	rt.loopMu.Unlock()

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	h, err := rt.pool.Acquire(ctx, "orchestrator", AcquireOptions{})
	if err != nil {
		t.Fatalf("Acquire: %v", err)
	}
	h.Model = "gemma"
	if !rt.SteerAgent(h.ID, "answer") {
		t.Fatal("SteerAgent did not find the orchestrator handle")
	}

	if h.Steering.Len() == 0 {
		t.Error("SteerAgent did not buffer steering in the handle")
	}

	select {
	case <-resume:
		t.Fatal("SteerAgent unexpectedly resumed a paused orchestrator loop")
	case <-time.After(50 * time.Millisecond):
	}
}

// TestRuntime_SteerAll_WithoutPendingUserDoesNotPanic ensures SteerAll is
// safe even when no loop is paused.
func TestRuntime_SteerAll_WithoutPendingUserDoesNotPanic(t *testing.T) {
	cfg := testRuntimeConfig()
	pool := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		return NewAgentHandle("fake-"+role, role, model), nil
	})
	rt, err := NewRuntime(cfg, pool, nil, "")
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	// No loop active; should not panic or block.
	rt.SteerAll("hello")
}

// TestRuntime_SteerOrchestrator_ResumesPausedLoop verifies the dedicated
// orchestrator steering path still resumes a paused loop.
func TestRuntime_SteerOrchestrator_ResumesPausedLoop(t *testing.T) {
	cfg := testRuntimeConfig()
	pool := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		return NewAgentHandle("fake-"+role, role, model), nil
	})
	rt, err := NewRuntime(cfg, pool, nil, "")
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	resume := make(chan struct{})
	rt.loopMu.Lock()
	rt.loopActive = true
	rt.pendingUser = true
	rt.resumeCh = resume
	rt.loopMu.Unlock()

	rt.SteerOrchestrator("answer")

	select {
	case <-resume:
		// resumed as expected
	case <-time.After(2 * time.Second):
		t.Fatal("SteerOrchestrator did not resume a paused orchestrator loop")
	}
}
func TestRuntimeRecordAgentEvents_ForwardThinkingToolCallResult(t *testing.T) {
	cfg := testRuntimeConfig()
	pool := NewBoundedAgentPool(cfg, func(role, model string, _ AcquireOptions) (*AgentHandle, error) {
		return NewAgentHandle("fake-"+role, role, model), nil
	})
	rt, err := NewRuntime(cfg, pool, nil, "")
	if err != nil {
		t.Fatalf("NewRuntime: %v", err)
	}

	h := NewAgentHandle("h-1", "coder", "gemma")
	rt.RecordAgentThinking(nil, "text")
	rt.RecordAgentThinking(h, "")
	rt.RecordAgentThinking(h, "reasoning")
	rt.RecordAgentToolCall(h, "writefile", `{"path":"x.txt"}`, "tc1", false)
	rt.RecordAgentToolResult(h, "tc1", "written", true)

	got := collectRuntimeEvents(t, rt)

	assertHasEventType(t, got, EventAgentThinking)
	assertHasEventType(t, got, EventAgentToolCall)
	assertHasEventType(t, got, EventAgentToolResult)

	for _, ev := range got {
		if ev.Type == EventAgentThinking || ev.Type == EventAgentToolCall || ev.Type == EventAgentToolResult {
			assertEventRoleAgent(t, ev, "coder", "h-1")
		}
		if ev.Type == EventAgentToolResult {
			assertToolResultPayload(t, ev, "written", true)
		}
	}
}

func testRuntimeConfig() config.OrchestratorConfig {
	return config.OrchestratorConfig{Roles: map[string]config.OrchestratorRole{"coder": {Model: "gemma"}}}
}

func eventTypesFor(evts []Event) []EventType {
	out := make([]EventType, len(evts))
	for i, ev := range evts {
		out[i] = ev.Type
	}
	return out
}
