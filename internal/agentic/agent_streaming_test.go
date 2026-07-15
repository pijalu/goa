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

// TestHandleToolCallDelta_AnthropicStyleNilPartial verifies the Anthropic
// streaming path: input_json_delta events carry only Delta + ContentIndex
// (no Partial snapshot). Previously these were dropped because the handler
// early-returned on Partial==nil, so Anthropic tool args never streamed to
// the TUI until the whole call completed. Now the delta is accumulated via
// the content-index index and re-emitted.
func TestHandleToolCallDelta_AnthropicStyleNilPartial(t *testing.T) {
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	ctx := context.Background()

	// content_block_start (tool_use): establishes id + name + index, no args yet.
	_, _, _ = agent.handleStreamEvent(ctx, nil, provider.AssistantMessageEvent{
		Type:         provider.EventToolCallStart,
		ContentIndex: 2,
		Partial: &provider.AssistantMessage{
			Content: []provider.ContentBlock{{
				Type:       provider.ContentBlockToolCall,
				ToolCallID: "toolu_anthropic_1",
				ToolName:   "write",
			}},
		},
	})

	// input_json_delta #1: Partial is nil, only Delta + ContentIndex.
	_, _, _ = agent.handleStreamEvent(ctx, nil, provider.AssistantMessageEvent{
		Type:         provider.EventToolCallDelta,
		ContentIndex: 2,
		Delta:        `{"path":"main.go","content":"pack`,
	})
	// input_json_delta #2.
	_, _, _ = agent.handleStreamEvent(ctx, nil, provider.AssistantMessageEvent{
		Type:         provider.EventToolCallDelta,
		ContentIndex: 2,
		Delta:        `age main"}`,
	})

	obs.mu.Lock()
	events := make([]OutputEvent, len(obs.events))
	copy(events, obs.events)
	obs.mu.Unlock()

	// Expect 1 (start, empty args) + 2 (delta accumulations) = 3 delta events.
	var deltas []OutputEvent
	for _, ev := range events {
		if ev.Type == EventToolCall && ev.IsDelta {
			deltas = append(deltas, ev)
		}
	}
	if len(deltas) != 3 {
		t.Fatalf("expected 3 streamed deltas, got %d", len(deltas))
	}
	last := deltas[len(deltas)-1]
	if last.ToolCallID != "toolu_anthropic_1" {
		t.Errorf("expected accumulated delta to keep id, got %q", last.ToolCallID)
	}
	if last.ToolInput != `{"path":"main.go","content":"package main"}` {
		t.Errorf("expected accumulated args, got %q", last.ToolInput)
	}
}

// TestHandleToolCallDelta_NilPartialNoIndexIsDropped ensures a nil-Partial
// delta with no prior content_block_start (unknown index) is safely ignored
// rather than panicking.
func TestHandleToolCallDelta_NilPartialNoIndexIsDropped(t *testing.T) {
	agent := NewAgent(Config{SystemPrompt: "test", Logger: NewLogger(Error)})
	obs := &mockEventObserver{}
	agent.AddObserver(obs)

	_, _, _ = agent.handleStreamEvent(context.Background(), nil, provider.AssistantMessageEvent{
		Type:         provider.EventToolCallDelta,
		ContentIndex: 9,
		Delta:        "orphan",
	})

	obs.mu.Lock()
	defer obs.mu.Unlock()
	if len(obs.events) != 0 {
		t.Errorf("expected no events for unknown-index delta, got %d", len(obs.events))
	}
}
