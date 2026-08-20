// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// runAgentCollectEvents runs one agent turn against the provider and returns
// the observed events.
func runAgentCollectEvents(t *testing.T, st *streamTestProvider, tools []Tool) []OutputEvent {
	t.Helper()
	provider.RegisterApiProvider(st)
	agent := NewAgent(Config{
		Model: provider.Model{
			ID:         "tool-start-test",
			Api:        st.API(),
			Provider:   provider.ProviderCustom,
			InputTypes: []string{"text"},
		},
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
		Tools:        tools,
	})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	ctx := context.Background()
	errCh := make(chan error, 1)
	go func() { errCh <- agent.Run(ctx, "go") }()
	go func() {
		for range agent.Output {
		}
	}()
	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for agent")
	}
	agent.Stop()
	return obs.Events()
}

// TestAgent_NamelessToolCallDeltaNotEmitted is the agent-side guard for
// Empty tool TUI: OpenAI-style streams ship the call id before the
// tool name; a nameless delta must NOT reach observers (the TUI would create
// a blank-header widget that never updates). The first emitted delta carries
// the name and the full accumulated args prefix.
func TestAgent_NamelessToolCallDeltaNotEmitted(t *testing.T) {
	st := &streamTestProvider{api: "test-nameless-tc", events: []provider.AssistantMessageEvent{
		// Chunk 1: id only, no name (OpenAI start chunk).
		{Type: provider.EventToolCallStart, ContentIndex: 1, Partial: &provider.AssistantMessage{
			Content: []provider.ContentBlock{{Type: provider.ContentBlockToolCall, ToolCallID: "call_1"}},
		}},
		// Chunk 2: name + first args — the first emit must happen HERE.
		{Type: provider.EventToolCallDelta, ContentIndex: 1, Partial: &provider.AssistantMessage{
			Content: []provider.ContentBlock{{Type: provider.ContentBlockToolCall, ToolCallID: "call_1", ToolName: "calculator", ToolArguments: `{"a":1`}},
		}},
		// Chunk 3: more args (Anthropic-style, index-correlated).
		{Type: provider.EventToolCallDelta, ContentIndex: 1, Delta: `,"b":2}`},
		// Final: the completed call.
		{Type: provider.EventToolCallEnd, ToolCall: &provider.ContentBlock{
			Type: provider.ContentBlockToolCall, ToolCallID: "call_1", ToolName: "calculator", ToolArguments: `{"a":1,"b":2}`,
		}},
	}}

	calcTool := mockTool{name: "calculator", schema: ToolSchema{
		Name: "calculator", Description: "math",
		Schema: map[string]any{"type": "object", "properties": map[string]any{}},
	}}
	events := runAgentCollectEvents(t, st, []Tool{calcTool})

	var deltas []OutputEvent
	for _, e := range events {
		if e.Type == EventToolCall && e.IsDelta {
			deltas = append(deltas, e)
		}
	}
	if len(deltas) == 0 {
		t.Fatal("expected named tool-call deltas to be emitted")
	}
	for i, d := range deltas {
		if d.ToolName == "" {
			t.Fatalf("delta %d has empty ToolName (blank widget bug): %+v", i, d)
		}
	}
	// The first emitted delta carries the name and the accumulated args prefix.
	if deltas[0].ToolName != "calculator" {
		t.Errorf("first delta name = %q, want calculator", deltas[0].ToolName)
	}
	if deltas[0].ToolInput == "" {
		t.Errorf("first delta should carry the accumulated args prefix, got empty")
	}
}

// TestAgent_EmitsToolStartBeforeResult verifies the scheduler-driven
// EventToolStart arrives for each executed call and before its result
// (Bug W).
func TestAgent_EmitsToolStartBeforeResult(t *testing.T) {
	st := &streamTestProvider{api: "test-tool-start", events: []provider.AssistantMessageEvent{
		{Type: provider.EventToolCallEnd, ToolCall: &provider.ContentBlock{
			Type: provider.ContentBlockToolCall, ToolCallID: "c1", ToolName: "calculator", ToolArguments: `{"a":1}`,
		}},
		{Type: provider.EventToolCallEnd, ToolCall: &provider.ContentBlock{
			Type: provider.ContentBlockToolCall, ToolCallID: "c2", ToolName: "calculator", ToolArguments: `{"a":2}`,
		}},
	}}

	calcTool := mockTool{name: "calculator", schema: ToolSchema{
		Name: "calculator", Description: "math",
		Schema: map[string]any{"type": "object", "properties": map[string]any{}},
	}}
	events := runAgentCollectEvents(t, st, []Tool{calcTool})

	startIdx := map[string]int{}
	resultIdx := map[string]int{}
	for i, e := range events {
		switch e.Type {
		case EventToolStart:
			startIdx[e.ToolCallID] = i
			if e.ToolName != "calculator" {
				t.Errorf("start event name = %q, want calculator", e.ToolName)
			}
		case EventToolResult:
			resultIdx[e.ToolCallID] = i
		}
	}
	for _, id := range []string{"c1", "c2"} {
		s, okS := startIdx[id]
		r, okR := resultIdx[id]
		if !okS {
			t.Fatalf("no EventToolStart for %s", id)
		}
		if !okR {
			t.Fatalf("no EventToolResult for %s", id)
		}
		if s >= r {
			t.Errorf("EventToolStart(%s) at %d must precede EventToolResult at %d", id, s, r)
		}
	}
}
