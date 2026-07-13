// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestHandleToolCallPartial_EmitsPartialEvent(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	// handleStreamEvent is the dispatcher; we call it directly to test
	// the EventToolCallStart handling path.
	_, _, _ = agent.handleStreamEvent(context.Background(), nil, provider.AssistantMessageEvent{
		Type: provider.EventToolCallStart,
		Partial: &provider.AssistantMessage{
			Content: []provider.ContentBlock{
				{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    "call_abc123",
					ToolName:      "write",
					ToolArguments: `{"path":"test.go","content":"package`,
				},
			},
		},
	})

	obs.mu.Lock()
	events := make([]OutputEvent, len(obs.events))
	copy(events, obs.events)
	obs.mu.Unlock()

	if len(events) == 0 {
		t.Fatal("expected at least one event from handleStreamEvent")
	}

	// The last event should be the tool call with IsDelta=true.
	last := events[len(events)-1]
	if last.Type != EventToolCall {
		t.Errorf("expected EventToolCall, got %v", last.Type)
	}
	if !last.IsDelta {
		t.Error("expected IsDelta=true for partial tool call event")
	}
	if last.ToolName != "write" {
		t.Errorf("expected ToolName=write, got %q", last.ToolName)
	}
	if last.ToolCallID != "call_abc123" {
		t.Errorf("expected ToolCallID=call_abc123, got %q", last.ToolCallID)
	}
	if last.ToolInput != `{"path":"test.go","content":"package` {
		t.Errorf("unexpected ToolInput: %q", last.ToolInput)
	}
}

func TestHandleToolCallPartial_AccumulatesMultipleDeltas(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	ctx := context.Background()

	// Simulate a sequence: Start → Delta → Delta.
	_, _, _ = agent.handleStreamEvent(ctx, nil, provider.AssistantMessageEvent{
		Type: provider.EventToolCallStart,
		Partial: &provider.AssistantMessage{
			Content: []provider.ContentBlock{
				{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    "call_delta_test",
					ToolName:      "write",
					ToolArguments: `{"path":"test.go","content":"pack`,
				},
			},
		},
	})

	_, _, _ = agent.handleStreamEvent(ctx, nil, provider.AssistantMessageEvent{
		Type:  provider.EventToolCallDelta,
		Delta: `age main`,
		Partial: &provider.AssistantMessage{
			Content: []provider.ContentBlock{
				{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    "call_delta_test",
					ToolName:      "write",
					ToolArguments: `{"path":"test.go","content":"package main`,
				},
			},
		},
	})

	_, _, _ = agent.handleStreamEvent(ctx, nil, provider.AssistantMessageEvent{
		Type:  provider.EventToolCallDelta,
		Delta: `

func main() {
\tprintln("hello")
}`,
		Partial: &provider.AssistantMessage{
			Content: []provider.ContentBlock{
				{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    "call_delta_test",
					ToolName:      "write",
					ToolArguments: `{"path":"test.go","content":"package main

func main() {
\tprintln("hello")
}`,
				},
			},
		},
	})

	obs.mu.Lock()
	events := make([]OutputEvent, len(obs.events))
	copy(events, obs.events)
	obs.mu.Unlock()

	// Count EventToolCall events.
	toolCallCount := 0
	for _, ev := range events {
		if ev.Type == EventToolCall && ev.IsDelta {
			toolCallCount++
		}
	}

	if toolCallCount != 3 {
		t.Errorf("expected 3 EventToolCall delta events, got %d", toolCallCount)
	}

	// Verify each event has IsDelta=true and proper ToolName.
	for _, ev := range events {
		if ev.Type == EventToolCall && ev.IsDelta {
			if ev.ToolName != "write" {
				t.Errorf("expected ToolName=write for delta event, got %q", ev.ToolName)
			}
		}
	}
}

func TestHandleToolCallPartial_IgnoresEmptyContentBlock(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	// Partial with nil content should be silently ignored.
	_, _, _ = agent.handleStreamEvent(context.Background(), nil, provider.AssistantMessageEvent{
		Type:    provider.EventToolCallStart,
		Partial: nil,
	})

	obs.mu.Lock()
	events := make([]OutputEvent, len(obs.events))
	copy(events, obs.events)
	obs.mu.Unlock()

	if len(events) > 0 {
		t.Errorf("expected no events for nil partial, got %d", len(events))
	}
}

func TestHandleToolCallPartial_IgnoresEmptyContentSlice(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	// Partial with empty content slice should be silently ignored.
	_, _, _ = agent.handleStreamEvent(context.Background(), nil, provider.AssistantMessageEvent{
		Type: provider.EventToolCallStart,
		Partial: &provider.AssistantMessage{
			Content: []provider.ContentBlock{},
		},
	})

	obs.mu.Lock()
	events := make([]OutputEvent, len(obs.events))
	copy(events, obs.events)
	obs.mu.Unlock()

	if len(events) > 0 {
		t.Errorf("expected no events for empty content, got %d", len(events))
	}
}
