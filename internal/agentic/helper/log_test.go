// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"encoding/json"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

func TestNewMessageLogObserver(t *testing.T) {
	obs := NewMessageLogObserver()
	if obs == nil {
		t.Fatal("NewMessageLogObserver returned nil")
	}
	if obs.pendingToolCalls == nil {
		t.Fatal("pendingToolCalls should be initialized")
	}
}

func TestMessageLogObserver_SystemUserEvents(t *testing.T) {
	obs := NewMessageLogObserver()

	// System and user messages now come via events
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.System, Text: "You are helpful"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "Hello"})

	history := obs.History()
	if len(history) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(history))
	}
	if history[0].Role != "system" {
		t.Errorf("expected role 'system', got %s", history[0].Role)
	}
	if history[0].Elements[0].Text != "You are helpful" {
		t.Errorf("expected text 'You are helpful', got %s", history[0].Elements[0].Text)
	}
	if history[1].Role != "user" {
		t.Errorf("expected role 'user', got %s", history[1].Role)
	}
}

func TestMessageLogObserver_ThinkingContent(t *testing.T) {
	obs := NewMessageLogObserver()

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "Let me think..."})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "Answer: 42"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	history := obs.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 message, got %d", len(history))
	}
	msg := history[0]
	if msg.Role != "assistant" {
		t.Errorf("expected assistant role, got %s", msg.Role)
	}
	if len(msg.Elements) != 2 {
		t.Fatalf("expected 2 elements, got %d: %+v", len(msg.Elements), msg.Elements)
	}
	if msg.Elements[0].Type != "thinking" {
		t.Errorf("expected thinking element, got %s", msg.Elements[0].Type)
	}
	if msg.Elements[0].Text != "Let me think..." {
		t.Errorf("expected thinking text, got %s", msg.Elements[0].Text)
	}
	if msg.Elements[1].Type != "content" {
		t.Errorf("expected content element, got %s", msg.Elements[1].Type)
	}
	if msg.Elements[1].Text != "Answer: 42" {
		t.Errorf("expected content text, got %s", msg.Elements[1].Text)
	}
}

func TestMessageLogObserver_DeltaAccumulation(t *testing.T) {
	obs := NewMessageLogObserver()

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "The "})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "answer"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: " is 27"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	history := obs.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 message, got %d", len(history))
	}
	if len(history[0].Elements) != 1 {
		t.Fatalf("expected 1 element, got %d", len(history[0].Elements))
	}
	if history[0].Elements[0].Text != "The answer is 27" {
		t.Errorf("expected accumulated text, got %s", history[0].Elements[0].Text)
	}
}

func TestMessageLogObserver_ToolCall(t *testing.T) {
	obs := NewMessageLogObserver()

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "I'll calculate"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateToolCall})
	obs.OnEvent(agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		ToolName:   "calculator",
		ToolInput:  `{"a":1,"b":1,"op":"+"}`,
		ToolCallID: "call_1",
	})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "Result: 2"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	history := obs.History()
	if len(history) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(history), history)
	}

	if len(history[0].Elements) != 1 || history[0].Elements[0].Type != "thinking" {
		t.Error("first message should have thinking element")
	}

	if len(history[1].Elements) != 1 || history[1].Elements[0].Type != "tool_call" {
		t.Error("second message should have tool_call element")
	}
	if history[1].Elements[0].ToolName != "calculator" {
		t.Errorf("expected tool name 'calculator', got %s", history[1].Elements[0].ToolName)
	}

	if len(history[2].Elements) != 1 || history[2].Elements[0].Type != "content" {
		t.Error("third message should have content element")
	}
}

func TestMessageLogObserver_ToolResultAssociation(t *testing.T) {
	obs := NewMessageLogObserver()

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateToolCall})
	obs.OnEvent(agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		ToolName:   "calculator",
		ToolInput:  `{"a":1,"b":1,"op":"+"}`,
		ToolCallID: "call_1",
	})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	obs.OnEvent(agentic.OutputEvent{
		Type:       agentic.EventToolResult,
		State:      agentic.StateToolResult,
		Role:       agentic.ToolRole,
		Text:       "2",
		ToolCallID: "call_1",
	})

	history := obs.History()
	if len(history) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(history), history)
	}

	assistant := history[0]
	if len(assistant.Elements) != 1 {
		t.Fatalf("expected 1 element, got %d", len(assistant.Elements))
	}
	if assistant.Elements[0].ToolResult != "2" {
		t.Errorf("expected tool result '2', got %s", assistant.Elements[0].ToolResult)
	}

	toolMsg := history[1]
	if toolMsg.Role != "tool" {
		t.Errorf("expected role 'tool', got %s", toolMsg.Role)
	}
	if len(toolMsg.Elements) != 1 {
		t.Fatalf("expected 1 element in tool message, got %d", len(toolMsg.Elements))
	}
	if toolMsg.Elements[0].Text != "2" {
		t.Errorf("expected text '2', got %s", toolMsg.Elements[0].Text)
	}
	if toolMsg.Elements[0].ToolCallID != "call_1" {
		t.Errorf("expected call_id 'call_1', got %s", toolMsg.Elements[0].ToolCallID)
	}
}

func TestMessageLogObserver_ToolResultWithoutPendingCall(t *testing.T) {
	obs := NewMessageLogObserver()

	obs.OnEvent(agentic.OutputEvent{
		Type:       agentic.EventToolResult,
		State:      agentic.StateToolResult,
		Role:       agentic.ToolRole,
		Text:       "orphan result",
		ToolCallID: "unknown",
	})

	history := obs.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 message, got %d", len(history))
	}
	if history[0].Role != "tool" {
		t.Errorf("expected tool role, got %s", history[0].Role)
	}
}

func TestMessageLogObserver_EndFinalizesCurrent(t *testing.T) {
	obs := NewMessageLogObserver()

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "Hello"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventEnd})

	history := obs.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 message after End, got %d", len(history))
	}
	if history[0].Elements[0].Text != "Hello" {
		t.Errorf("expected text 'Hello', got %s", history[0].Elements[0].Text)
	}
}

func TestMessageLogObserver_ClearResets(t *testing.T) {
	obs := NewMessageLogObserver()

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.System, Text: "sys"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "hello"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventClear})

	history := obs.History()
	if len(history) != 0 {
		t.Errorf("expected empty history after clear, got %d", len(history))
	}
}

func TestMessageLogObserver_JSON(t *testing.T) {
	obs := NewMessageLogObserver()
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.User, Text: "Hello"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "Hi there"})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	data, err := obs.JSON()
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}

	var result []StructuredMessage
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(result) != 2 {
		t.Errorf("expected 2 messages, got %d", len(result))
	}
	if result[0].Role != "user" {
		t.Errorf("expected first role 'user', got %s", result[0].Role)
	}
	if result[1].Role != "assistant" {
		t.Errorf("expected second role 'assistant', got %s", result[1].Role)
	}
}

func TestMessageLogObserver_JSONEmpty(t *testing.T) {
	obs := NewMessageLogObserver()
	data, err := obs.JSON()
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}
	if string(data) != "[]" {
		t.Errorf("expected empty JSON array, got %s", string(data))
	}
}

func TestMessageLogObserver_TokenStats(t *testing.T) {
	obs := NewMessageLogObserver()

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "Hello"})
	obs.OnEvent(agentic.OutputEvent{
		Type: agentic.EventTokenStats,
		Timings: &agentic.TokenTimings{
			PromptN:            10,
			PredictedN:         5,
			PredictedPerSecond: 25.0,
		},
	})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	history := obs.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 message, got %d", len(history))
	}
	if history[0].Timings == nil {
		t.Fatal("expected Timings to be set on message")
	}
	if history[0].Timings.PromptN != 10 {
		t.Errorf("expected PromptN=10, got %d", history[0].Timings.PromptN)
	}
}

func TestMessageLogObserver_Progress(t *testing.T) {
	obs := NewMessageLogObserver()

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "Hello"})
	obs.OnEvent(agentic.OutputEvent{
		Type: agentic.EventProgress,
		PromptProgress: &agentic.PromptProgress{
			Total:     24,
			Processed: 12,
		},
	})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	history := obs.History()
	if len(history) != 1 {
		t.Fatalf("expected 1 message, got %d", len(history))
	}
	if history[0].PromptProgress == nil {
		t.Fatal("expected PromptProgress to be set on message")
	}
	if history[0].PromptProgress.Total != 24 {
		t.Errorf("expected Total=24, got %d", history[0].PromptProgress.Total)
	}
}

func TestMessageLogObserver_JSONWithStats(t *testing.T) {
	obs := NewMessageLogObserver()

	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventContent, Text: "Hello"})
	obs.OnEvent(agentic.OutputEvent{
		Type: agentic.EventTokenStats,
		Timings: &agentic.TokenTimings{
			PromptN:            10,
			PredictedN:         5,
			PredictedPerSecond: 25.0,
		},
	})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateIdle})

	data, err := obs.JSON()
	if err != nil {
		t.Fatalf("JSON() error: %v", err)
	}

	var result []StructuredMessage
	if err := json.Unmarshal(data, &result); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}
	if len(result) != 1 {
		t.Fatalf("expected 1 message, got %d", len(result))
	}
	if result[0].Timings == nil {
		t.Fatal("expected Timings in JSON output")
	}
	if result[0].Timings.PredictedPerSecond != 25.0 {
		t.Errorf("expected PredictedPerSecond=25.0, got %f", result[0].Timings.PredictedPerSecond)
	}
}
