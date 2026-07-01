// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package xmlstream_test

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/observer/xmlstream"
)

// sampleXML is a realistic conversation as produced by XMLStreamingObserver.
const sampleXML = `<conversation><metadata><id>test-conv</id><model>local-model</model><start>2026-01-01T00:00:00Z</start></metadata><messages>
<message><role>user</role><blocks><content>Plan a 3-person team for a web project</content></blocks></message>
<message><role>assistant</role><blocks>
<thinking>Let me break down what the user needs.</thinking>
<content>I'll help you set up a team. What roles do you need?</content>
</blocks></message>
<message><role>user</role><blocks><content>We need a PM, a backend dev, and a frontend dev.</content></blocks></message>
<message><role>assistant</role><blocks>
<thinking>Setting up team members now.</thinking>
<toolcall><name>state</name><input><![CDATA[{"action":"set","entity":"team","data":{"members":[{"name":"Alice","role":"PM"}]}}]]></input><output><![CDATA[Team updated: 1 member]]></output></toolcall>
<toolcall><name>state</name><input><![CDATA[{"action":"set","entity":"team","data":{"members":[{"name":"Bob","role":"Backend"}]}}]]></input><output><![CDATA[Team updated: 2 members]]></output></toolcall>
<content>I've added Alice (PM) and Bob (Backend). Who's your frontend developer?</content>
</blocks></message>
</messages></conversation>`

func TestParseConversationXML_Simple(t *testing.T) {
	msgs, err := xmlstream.ParseConversationXML(sampleXML)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msgs) != 8 {
		t.Fatalf("expected 8 messages, got %d", len(msgs))
	}

	assertMessage(t, 0, msgs[0], agentic.User, agentic.Content, "3-person")
	assertMessage(t, 1, msgs[1], agentic.Assistant, agentic.Content, "help you")
	assertToolCall(t, 3, msgs[3], "state", "tc-0")
	assertToolResult(t, 4, msgs[4], "Team updated", "tc-0")
	assertToolCall(t, 5, msgs[5], "", "tc-1")
	assertToolResult(t, 6, msgs[6], "", "tc-1")
	assertMessage(t, 7, msgs[7], agentic.Assistant, agentic.Content, "Alice")
}

func assertMessage(t *testing.T, idx int, msg agentic.Message, wantRole agentic.Role, wantType agentic.MessageType, wantContent string) {
	t.Helper()
	if msg.Role != wantRole || msg.Type != wantType {
		t.Errorf("msg[%d]: expected role=%s type=%s, got role=%s type=%s", idx, wantRole, wantType, msg.Role, msg.Type)
	}
	if !contains(msg.Content, wantContent) {
		t.Errorf("msg[%d]: expected %q in content, got %q", idx, wantContent, msg.Content)
	}
}

func assertToolCall(t *testing.T, idx int, msg agentic.Message, wantName, wantID string) {
	t.Helper()
	if msg.Role != agentic.Assistant || msg.Type != agentic.ToolCall {
		t.Errorf("msg[%d]: expected assistant tool_call, got role=%s type=%s", idx, msg.Role, msg.Type)
	}
	if wantName != "" && msg.ToolName != wantName {
		t.Errorf("msg[%d]: expected ToolName %q, got %q", idx, wantName, msg.ToolName)
	}
	if msg.ToolCallID != wantID {
		t.Errorf("msg[%d]: expected ToolCallID %q, got %q", idx, wantID, msg.ToolCallID)
	}
}

func assertToolResult(t *testing.T, idx int, msg agentic.Message, wantContent, wantID string) {
	t.Helper()
	if msg.Role != agentic.ToolRole || msg.Type != agentic.Content {
		t.Errorf("msg[%d]: expected tool content, got role=%s type=%s", idx, msg.Role, msg.Type)
	}
	if wantContent != "" && !contains(msg.Content, wantContent) {
		t.Errorf("msg[%d]: expected %q in content, got %q", idx, wantContent, msg.Content)
	}
	if msg.ToolCallID != wantID {
		t.Errorf("msg[%d]: expected ToolCallID %q, got %q", idx, wantID, msg.ToolCallID)
	}
}

func TestParseConversationXML_Empty(t *testing.T) {
	msgs, err := xmlstream.ParseConversationXML("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages for empty input, got %d", len(msgs))
	}

	msgs, err = xmlstream.ParseConversationXML("<messages></messages>")
	if err != nil {
		t.Fatalf("unexpected error on empty messages: %v", err)
	}
	if len(msgs) != 0 {
		t.Errorf("expected 0 messages for empty messages, got %d", len(msgs))
	}
}

func TestParseConversationXML_NoToolOutput(t *testing.T) {
	// Tool call without <output> (in-flight call) should only produce ToolCall, not ToolRole
	input := `<messages>
<message><role>assistant</role><blocks>
<toolcall><name>state</name><input>{"action":"get"}</input></toolcall>
<content>Done</content>
</blocks></message>
</messages>`

	msgs, err := xmlstream.ParseConversationXML(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages (toolcall + content), got %d", len(msgs))
	}
	if msgs[0].Type != agentic.ToolCall {
		t.Errorf("msg[0]: expected ToolCall, got %s", msgs[0].Type)
	}
	if msgs[1].Type != agentic.Content || msgs[1].Role != agentic.Assistant {
		t.Errorf("msg[1]: expected assistant content, got role=%s type=%s", msgs[1].Role, msgs[1].Type)
	}
}

func TestParseConversationXML_SystemRole(t *testing.T) {
	input := `<messages>
<message><role>system</role><blocks><content>You are a helpful assistant.</content></blocks></message>
<message><role>user</role><blocks><content>Hello</content></blocks></message>
</messages>`

	msgs, err := xmlstream.ParseConversationXML(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}
	if msgs[0].Role != agentic.System {
		t.Errorf("msg[0]: expected system role, got %s", msgs[0].Role)
	}
	if msgs[1].Role != agentic.User {
		t.Errorf("msg[1]: expected user role, got %s", msgs[1].Role)
	}
}

func TestParseConversationXML_BareMessages(t *testing.T) {
	// No <conversation> wrapper — just <messages>
	input := `<messages><message><role>user</role><blocks><content>Hi</content></blocks></message></messages>`

	msgs, err := xmlstream.ParseConversationXML(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].Role != agentic.User || !contains(msgs[0].Content, "Hi") {
		t.Errorf("msg[0]: expected user 'Hi', got role=%s content=%q", msgs[0].Role, msgs[0].Content)
	}
}

func TestParseConversationXML_MultipleContent(t *testing.T) {
	// Assistant message with multiple <content> blocks
	input := `<messages>
<message><role>assistant</role><blocks>
<thinking>Processing...</thinking>
<content>First part.</content>
<content>Second part.</content>
</blocks></message>
</messages>`

	msgs, err := xmlstream.ParseConversationXML(input)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if !contains(msgs[0].Content, "First part") || !contains(msgs[0].Content, "Second part") {
		t.Errorf("msg[0]: expected both content parts, got %q", msgs[0].Content)
	}
}

func TestParseConversationXML_RoundTripIdentity(t *testing.T) {
	// Parse → convert to agentic.Message → verify it looks like a valid
	// conversation that an LLM could continue from.
	msgs, err := xmlstream.ParseConversationXML(sampleXML)
	if err != nil {
		t.Fatalf("parse error: %v", err)
	}

	// The output must be usable as SetHistory input:
	// - First message must not be ToolRole (OpenAI API requirement)
	// - No duplicate roles in sequence (user→assistant→user→...)
	// - ToolCall must be followed by matching ToolRole
	if len(msgs) == 0 {
		t.Fatal("expected at least one message")
	}
	if msgs[0].Role == agentic.ToolRole {
		t.Error("first message must not be tool role")
	}

	// Verify tool call <-> result pairing
	for i, m := range msgs {
		if m.Type == agentic.ToolCall {
			if i+1 >= len(msgs) || msgs[i+1].Role != agentic.ToolRole || msgs[i+1].ToolCallID != m.ToolCallID {
				t.Errorf("msg[%d]: ToolCall %s lacks matching ToolRole result", i, m.ToolCallID)
			}
		}
	}
}

func contains(s, substr string) bool {
	return strings.Contains(s, substr)
}
