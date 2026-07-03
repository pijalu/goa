// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"testing"
)

func TestEventsToHistory_Empty(t *testing.T) {
	got := EventsToHistory(nil)
	if len(got) != 0 {
		t.Errorf("EventsToHistory(nil) = %d messages, want 0", len(got))
	}
}

func TestEventsToHistory_EmptyEvents(t *testing.T) {
	got := EventsToHistory([]OutputEvent{})
	if len(got) != 0 {
		t.Errorf("EventsToHistory([]) = %d messages, want 0", len(got))
	}
}

func TestEventsToHistory_UserMessage(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "hello"},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d", len(got))
	}
	if got[0].Role != User || got[0].Content != "hello" {
		t.Errorf("expected User message 'hello', got Role=%s Content=%q", got[0].Role, got[0].Content)
	}
}

func TestEventsToHistory_AssistantContent(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "tell me"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "Sure, ", IsDelta: true},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "here it is!", IsDelta: true},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[0].Role != User || got[0].Content != "tell me" {
		t.Errorf("expected user message, got Role=%s Content=%q", got[0].Role, got[0].Content)
	}
	if got[1].Role != Assistant || got[1].Content != "Sure, here it is!" {
		t.Errorf("expected assistant message 'Sure, here it is!', got Role=%s Content=%q", got[1].Role, got[1].Content)
	}
	if len(got[1].ToolCalls) != 0 {
		t.Errorf("expected no tool calls, got %d", len(got[1].ToolCalls))
	}
}

func TestEventsToHistory_ThinkingContent(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "think"},
		{Type: EventContent, Role: Assistant, State: StateThinking, Text: "let me", IsDelta: true},
		{Type: EventContent, Role: Assistant, State: StateThinking, Text: " think...", IsDelta: true},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "Answer:", IsDelta: true},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(got))
	}
	if got[1].Role != Assistant {
		t.Errorf("expected assistant role, got %s", got[1].Role)
	}
	if got[1].Thinking != "let me think..." {
		t.Errorf("expected thinking 'let me think...', got %q", got[1].Thinking)
	}
	if got[1].Content != "Answer:" {
		t.Errorf("expected content 'Answer:', got %q", got[1].Content)
	}
}

func TestEventsToHistory_ToolCallAndResult(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "run command"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "Running:", IsDelta: true},
		{Type: EventToolCall, ToolName: "bash", ToolInput: `{"cmd":"ls"}`, ToolCallID: "call1"},
		{Type: EventToolResult, ToolName: "bash", ToolResult: "file1\nfile2", ToolCallID: "call1"},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
	// Assistant message with content + tool call
	if got[1].Role != Assistant || got[1].Content != "Running:" {
		t.Errorf("expected assistant content 'Running:', got Role=%s Content=%q", got[1].Role, got[1].Content)
	}
	if len(got[1].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(got[1].ToolCalls))
	}
	if got[1].ToolCalls[0].Name != "bash" || got[1].ToolCalls[0].Arguments != `{"cmd":"ls"}` {
		t.Errorf("tool call mismatch: %+v", got[1].ToolCalls[0])
	}
	// Tool result
	if got[2].Role != ToolRole || got[2].Content != "file1\nfile2" {
		t.Errorf("expected tool result, got Role=%s Content=%q", got[2].Role, got[2].Content)
	}
}

func TestEventsToHistory_SkipsNonMessageEvents(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "hello"},
		{Type: EventTokenStats, Timings: &TokenTimings{PromptN: 10}},
		{Type: EventContextStats, ContextStats: &ContextStats{EstimatedTokens: 100}},
		{Type: EventCompact, Text: "micro"},
		{Type: EventProgress, Text: "processing..."},
		{Type: EventStateChange, State: StateContent},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "world", IsDelta: true},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (user + assistant), got %d", len(got))
	}
	if got[0].Content != "hello" || got[1].Content != "world" {
		t.Errorf("unexpected content: %q, %q", got[0].Content, got[1].Content)
	}
}

func TestEventsToHistory_SkipsSystemMessages(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: System, Text: "system prompt"},
		{Type: EventContent, Role: User, Text: "hi"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "hello!"},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (no system), got %d", len(got))
	}
	if got[0].Role != User || got[1].Role != Assistant {
		t.Errorf("expected User then Assistant, got %s then %s", got[0].Role, got[1].Role)
	}
}

func TestEventsToHistory_MultipleTurns(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "first"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "answer 1"},
		{Type: EventEnd},
		{Type: EventContent, Role: User, Text: "second"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "answer 2"},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(got))
	}
	if got[0].Content != "first" || got[1].Content != "answer 1" {
		t.Errorf("turn 1: %q, %q", got[0].Content, got[1].Content)
	}
	if got[2].Content != "second" || got[3].Content != "answer 2" {
		t.Errorf("turn 2: %q, %q", got[2].Content, got[3].Content)
	}
}

func TestEventsToHistory_NoEventEnd(t *testing.T) {
	events := []OutputEvent{
		{Type: EventContent, Role: User, Text: "hi"},
		{Type: EventContent, Role: Assistant, State: StateContent, Text: "hey there"},
	}
	got := EventsToHistory(events)
	if len(got) != 2 {
		t.Fatalf("expected 2 messages even without EventEnd, got %d", len(got))
	}
	if got[1].Content != "hey there" {
		t.Errorf("expected assistant content, got %q", got[1].Content)
	}
}

func TestEventsToHistory_EmptyToolResult(t *testing.T) {
	events := []OutputEvent{
		{Type: EventToolCall, ToolName: "bash", ToolInput: "ls", ToolCallID: "c1"},
		{Type: EventToolResult, ToolCallID: "c1", ToolResult: ""},
		{Type: EventEnd},
	}
	got := EventsToHistory(events)
	// Tool call without preceding content should still produce assistant + tool messages
	if len(got) != 2 {
		t.Fatalf("expected 2 messages (assistant+tool), got %d", len(got))
	}
	if got[0].Role != Assistant || len(got[0].ToolCalls) != 1 {
		t.Errorf("expected assistant with 1 tool call, got Role=%s toolCalls=%d", got[0].Role, len(got[0].ToolCalls))
	}
	if got[1].Role != ToolRole || got[1].ToolCallID != "c1" {
		t.Errorf("expected tool role message, got Role=%s", got[1].Role)
	}
}
