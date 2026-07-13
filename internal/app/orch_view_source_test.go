// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"reflect"
	"strings"
	"testing"

	"github.com/pijalu/goa/core/orchestrator"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
	"github.com/pijalu/goa/tui"
)

// TestTranslateOrchEvent_Mappings is a table-driven assertion that every
// orchestrator event kind maps to the expected neutral AgentViewEvent
// (including cache_read → Stats.CacheRead and provider/thinking passthrough),
// and that unknown kinds return ok=false. This is the end-to-end proof of the
// only orchestration-specific seam.
func TestTranslateOrchEvent_Mappings(t *testing.T) {
	cases := []struct {
		name string
		in   orchestrator.Event
		want orchpanel.AgentViewEvent
	}{
		{
			name: "run_started carries objective/topology meta",
			in:   orchestrator.Event{Type: orchestrator.EventRunStarted, Payload: map[string]any{"objective": "o", "topology": "hub", "name": "happy"}},
			want: orchpanel.AgentViewEvent{Kind: orchpanel.EvSourceStarted, Meta: map[string]string{"objective": "o", "topology": "hub", "name": "happy"}},
		},
		{
			name: "run_finished ok",
			in:   orchestrator.Event{Type: orchestrator.EventRunFinished, Payload: map[string]any{"ok": true}},
			want: orchpanel.AgentViewEvent{Kind: orchpanel.EvSourceFinished, Status: "ok"},
		},
		{
			name: "run_finished failed",
			in:   orchestrator.Event{Type: orchestrator.EventRunFinished, Payload: map[string]any{"ok": false}},
			want: orchpanel.AgentViewEvent{Kind: orchpanel.EvSourceFinished, Status: "failed"},
		},
		{
			name: "agent_started passes provider/thinking",
			in: orchestrator.Event{Type: orchestrator.EventAgentStarted, AgentID: "c-1", Role: "coder", Model: "gemma",
				Payload: map[string]any{"provider": "google", "thinking": "off"}},
			want: orchpanel.AgentViewEvent{Kind: orchpanel.EvAgentStarted, AgentID: "c-1", Role: "coder", Model: "gemma",
				Provider: "google", Thinking: "off"},
		},
		{
			name: "agent_thinking passes text",
			in:   orchestrator.Event{Type: orchestrator.EventAgentThinking, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "reasoning"}},
			want: orchpanel.AgentViewEvent{Kind: orchpanel.EvAgentThinking, AgentID: "c-1", Role: "coder", Text: "reasoning"},
		},
		{
			name: "agent_tool_call passes tool/input/call_id",
			in:   orchestrator.Event{Type: orchestrator.EventAgentToolCall, AgentID: "c-1", Role: "coder", Payload: map[string]any{"tool": "writefile", "input": `{"path":"x.txt"}`, "call_id": "t1"}},
			want: orchpanel.AgentViewEvent{Kind: orchpanel.EvAgentToolCall, AgentID: "c-1", Role: "coder", Tool: "writefile", ToolInput: `{"path":"x.txt"}`, CallID: "t1"},
		},
		{
			name: "agent_tool_call delta passes is_delta",
			in:   orchestrator.Event{Type: orchestrator.EventAgentToolCall, AgentID: "c-1", Role: "coder", Payload: map[string]any{"tool": "writefile", "input": `{"path":"x`, "call_id": "t1", "is_delta": true}},
			want: orchpanel.AgentViewEvent{Kind: orchpanel.EvAgentToolCall, AgentID: "c-1", Role: "coder", Tool: "writefile", ToolInput: `{"path":"x`, CallID: "t1", IsDelta: true},
		},
		{
			name: "ask_user passes question",
			in:   orchestrator.Event{Type: orchestrator.EventAskUser, AgentID: "o-1", Role: "orchestrator", Payload: map[string]any{"question": "What is the goal?"}},
			want: orchpanel.AgentViewEvent{Kind: orchpanel.EvAskUser, AgentID: "o-1", Role: "orchestrator", Question: "What is the goal?"},
		},
		{
			name: "agent_tool_result passes call_id/text/ok",
			in:   orchestrator.Event{Type: orchestrator.EventAgentToolResult, AgentID: "c-1", Role: "coder", Payload: map[string]any{"call_id": "t1", "text": "written", "ok": true}},
			want: orchpanel.AgentViewEvent{Kind: orchpanel.EvAgentToolResult, AgentID: "c-1", Role: "coder", CallID: "t1", Text: "written", OK: true},
		},
		{
			name: "agent_message passes text",
			in:   orchestrator.Event{Type: orchestrator.EventAgentMessage, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "hi"}},
			want: orchpanel.AgentViewEvent{Kind: orchpanel.EvAgentMessage, AgentID: "c-1", Role: "coder", Text: "hi"},
		},
		{
			name: "agent_stats carries cache_read and thinking",
			in: orchestrator.Event{Type: orchestrator.EventAgentStats, AgentID: "c-1", Role: "coder",
				Payload: map[string]any{"turns": 2, "tokens_in": 40, "tokens_out": 12, "cache_read": 1024,
					"cache_creation": 8, "tool_calls": 3, "status": "running", "thinking": "medium"}},
			want: orchpanel.AgentViewEvent{Kind: orchpanel.EvAgentStats, AgentID: "c-1", Role: "coder",
				Status: "running", Thinking: "medium",
				Stats: &orchpanel.AgentStatsDelta{Turns: 2, TokensIn: 40, TokensOut: 12, CacheRead: 1024, CacheCreation: 8, ToolCalls: 3}},
		},
		{
			name: "agent_steered passes text",
			in:   orchestrator.Event{Type: orchestrator.EventAgentSteered, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "fix"}},
			want: orchpanel.AgentViewEvent{Kind: orchpanel.EvAgentSteered, AgentID: "c-1", Role: "coder", Text: "fix"},
		},
		{
			name: "agent_finished outcome",
			in:   orchestrator.Event{Type: orchestrator.EventAgentFinished, AgentID: "c-1", Role: "coder", Payload: map[string]any{"outcome": "crashed"}},
			want: orchpanel.AgentViewEvent{Kind: orchpanel.EvAgentFinished, AgentID: "c-1", Role: "coder", Status: "crashed"},
		},
	}

	for _, tc := range cases {
		got, ok := translateOrchEvent(tc.in)
		if !ok {
			t.Errorf("%s: translateOrchEvent returned ok=false for %s", tc.name, tc.in.Type)
			continue
		}
		if !reflect.DeepEqual(got, tc.want) {
			t.Errorf("%s: translateOrchEvent = %+v, want %+v", tc.name, got, tc.want)
		}
	}
}

// TestTranslateOrchEvent_UnknownReturnsFalse asserts non-mapped event kinds
// report ok=false so the forwarder can ignore them.
func TestTranslateOrchEvent_UnknownReturnsFalse(t *testing.T) {
	if _, ok := translateOrchEvent(orchestrator.Event{Type: "totally_unknown"}); ok {
		t.Errorf("expected ok=false for unknown event type")
	}
}

// TestDisplayOrchestratorQuestion_AddsSystemMessage verifies that an ask_user
// event surfaces the question as a system chat message.
func TestDisplayOrchestratorQuestion_AddsSystemMessage(t *testing.T) {
	a := &App{}
	a.subs = &subsystems{chat: tui.NewChatViewport()}

	a.displayOrchestratorQuestion(orchpanel.AgentViewEvent{
		Kind:     orchpanel.EvAskUser,
		AgentID:  "o-1",
		Role:     "orchestrator",
		Question: "What is the goal?",
	})

	msgs := a.subs.chat.Messages()
	found := false
	for _, m := range msgs {
		if m.Type == tui.ConsoleSystemMessage && strings.Contains(m.Content, "What is the goal?") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected system message with question, got messages: %v", msgs)
	}
}

// TestDisplayOrchestratorQuestion_EmptyQuestionIsNoOp verifies that an empty
// question does not add a message.
func TestDisplayOrchestratorQuestion_EmptyQuestionIsNoOp(t *testing.T) {
	a := &App{}
	a.subs = &subsystems{chat: tui.NewChatViewport()}

	a.displayOrchestratorQuestion(orchpanel.AgentViewEvent{
		Kind:     orchpanel.EvAskUser,
		AgentID:  "o-1",
		Role:     "orchestrator",
		Question: "",
	})

	if len(a.subs.chat.Messages()) != 0 {
		t.Errorf("expected no messages for empty question, got %d", len(a.subs.chat.Messages()))
	}
}

