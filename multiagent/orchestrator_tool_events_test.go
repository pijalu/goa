// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

const toolEventsTimeout = 2 * time.Second

// TestHandleAgentOutputEvent_ToolEventsEmitted verifies team UI bug RC-2 fix:
// sub-agent tool calls and results are forwarded as orchestrator events
// (kind tool_call/tool_result) so the TUI can show real sub-agent activity
// instead of an apparently frozen "thinking..." section.
func TestHandleAgentOutputEvent_ToolEventsEmitted(t *testing.T) {
	pool := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	state := &agentOutputState{}

	// Drain events in the background — emitKind blocks when the buffer fills.
	done, getEvents := collectEventsUntil(orch, toolEventsTimeout, func(ev OrchestratorMessage) bool {
		return ev.Kind == "tool_result"
	})

	handleAgentOutputEvent(orch, "coder", state, agentic.OutputEvent{
		Type: agentic.EventToolCall, ToolName: "search", ToolCallID: "c1",
	})
	handleAgentOutputEvent(orch, "coder", state, agentic.OutputEvent{
		Type: agentic.EventToolResult, Text: "76 matches across 63 files", ToolCallID: "c1",
	})
	<-done

	events := getEvents()
	var call, result *OrchestratorMessage
	for i := range events {
		switch events[i].Kind {
		case "tool_call":
			call = &events[i]
		case "tool_result":
			result = &events[i]
		}
	}
	if call == nil {
		t.Fatal("no tool_call event emitted")
	}
	if call.From != "coder" || call.To != "tool_call" || call.Content != "search" {
		t.Errorf("tool_call event wrong: %+v", *call)
	}
	if result == nil {
		t.Fatal("no tool_result event emitted")
	}
	if result.From != "coder" || result.To != "tool_result" {
		t.Errorf("tool_result event wrong: %+v", *result)
	}
	if result.Content != "76 matches across 63 files" {
		t.Errorf("tool_result preview = %q", result.Content)
	}
}

// TestHandleAgentOutputEvent_PerRoleStateIsolation verifies the observer state
// machine keeps per-role buffers: content streamed under role A never lands in
// role B's stream events (team UI bug RC-1 at the emission layer).
func TestHandleAgentOutputEvent_PerRoleStateIsolation(t *testing.T) {
	pool := NewAgentPool(testModel("default"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)
	stateA := &agentOutputState{}
	stateB := &agentOutputState{}

	done, getEvents := collectEventsUntil(orch, toolEventsTimeout, func(ev OrchestratorMessage) bool {
		return ev.To == "stream_end" && ev.From == "coder"
	})

	handleAgentOutputEvent(orch, "planner", stateA, agentic.OutputEvent{
		Type: agentic.EventContent, Role: agentic.Assistant, Text: "planner text",
	})
	handleAgentOutputEvent(orch, "coder", stateB, agentic.OutputEvent{
		Type: agentic.EventContent, Role: agentic.Assistant, Text: "coder text",
	})
	handleAgentOutputEvent(orch, "planner", stateA, agentic.OutputEvent{Type: agentic.EventEnd})
	handleAgentOutputEvent(orch, "coder", stateB, agentic.OutputEvent{Type: agentic.EventEnd})
	<-done

	for _, ev := range getEvents() {
		if ev.Kind != "content" || ev.To != "stream_chunk" {
			continue
		}
		if ev.From == "planner" && ev.Content != "planner text" {
			t.Errorf("planner chunk carries wrong content: %q", ev.Content)
		}
		if ev.From == "coder" && ev.Content != "coder text" {
			t.Errorf("coder chunk carries wrong content: %q", ev.Content)
		}
	}
}

// TestToolResultPreview covers the single-line truncation rules.
func TestToolResultPreview(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"one line", "one line"},
		{"first\nsecond", "first"},
		{"  padded  ", "padded"},
	}
	for _, c := range cases {
		if got := toolResultPreview(c.in); got != c.want {
			t.Errorf("toolResultPreview(%q) = %q, want %q", c.in, got, c.want)
		}
	}
	long := make([]byte, 100)
	for i := range long {
		long[i] = 'x'
	}
	// 80 bytes + "…" (3 bytes UTF-8) = 83.
	if got := toolResultPreview(string(long)); len(got) != 83 {
		t.Errorf("toolResultPreview long len = %d, want 83", len(got))
	}
}
