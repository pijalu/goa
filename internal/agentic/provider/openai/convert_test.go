// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package openai

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func TestConvertToolResultMessage_Default(t *testing.T) {
	msg := provider.NewToolResultMessage("call_1", "calculator", "15", false)
	compat := provider.OpenAICompletionsCompat{}

	got := convertToolResultMessage(msg, compat)

	if got["role"] != "tool" {
		t.Errorf("expected role tool, got %q", got["role"])
	}
	if got["content"] != "15" {
		t.Errorf("expected content 15, got %q", got["content"])
	}
	if got["tool_call_id"] != "call_1" {
		t.Errorf("expected tool_call_id call_1, got %q", got["tool_call_id"])
	}
}

func TestConvertToolResultMessage_AsUser(t *testing.T) {
	msg := provider.NewToolResultMessage("call_1", "calculator", "15", false)
	compat := provider.OpenAICompletionsCompat{
		ToolResultAsUser: provider.BoolPtr(true),
	}

	got := convertToolResultMessage(msg, compat)

	if got["role"] != "user" {
		t.Errorf("expected role user, got %q", got["role"])
	}
	content, ok := got["content"].(string)
	if !ok {
		t.Fatalf("expected string content, got %T", got["content"])
	}
	if content == "" {
		t.Error("expected non-empty formatted content")
	}
	if content == "15" {
		t.Error("expected XML-wrapped content, got raw result")
	}
}

func TestConvertAssistantMessage_WithToolCalls(t *testing.T) {
	msg := provider.NewAssistantMessage([]provider.ContentBlock{
		{Type: provider.ContentBlockToolCall, ToolCallID: "call_1", ToolName: "read", ToolArguments: `{"path":"PLAN.md"}`},
		{Type: provider.ContentBlockText, Text: ""},
	})
	compat := provider.OpenAICompletionsCompat{}

	got := convertAssistantMessage(msg, compat)

	if got["role"] != "assistant" {
		t.Errorf("expected role assistant, got %q", got["role"])
	}
	toolCalls, ok := got["tool_calls"].([]map[string]interface{})
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool_call, got %+v", got["tool_calls"])
	}
	if toolCalls[0]["id"] != "call_1" {
		t.Errorf("expected tool_call id call_1, got %q", toolCalls[0]["id"])
	}
	fn, ok := toolCalls[0]["function"].(map[string]interface{})
	if !ok {
		t.Fatal("expected function map")
	}
	if fn["name"] != "read" {
		t.Errorf("expected function name read, got %q", fn["name"])
	}
}

func TestConvertAssistantMessage_SanitizesMalformedToolArguments(t *testing.T) {
	// Regression test for the poolside 400 "Invalid JSON in tool call
	// arguments": a tool call truncated mid-stream must re-serialize as
	// valid JSON or the provider rejects the whole request.
	msg := provider.NewAssistantMessage([]provider.ContentBlock{
		{Type: provider.ContentBlockToolCall, ToolCallID: "call_1", ToolName: "edit",
			ToolArguments: `{"path": "/tmp/a.md", "old_string": "a", "new_string": "b"`},
	})
	compat := provider.OpenAICompletionsCompat{}

	got := convertAssistantMessage(msg, compat)

	toolCalls, ok := got["tool_calls"].([]map[string]interface{})
	if !ok || len(toolCalls) != 1 {
		t.Fatalf("expected 1 tool_call, got %+v", got["tool_calls"])
	}
	fn, ok := toolCalls[0]["function"].(map[string]interface{})
	if !ok {
		t.Fatal("expected function map")
	}
	args, ok := fn["arguments"].(string)
	if !ok {
		t.Fatalf("expected string arguments, got %T", fn["arguments"])
	}
	if !json.Valid([]byte(args)) {
		t.Fatalf("arguments must be valid JSON, got %q", args)
	}
	var parsed map[string]interface{}
	if err := json.Unmarshal([]byte(args), &parsed); err != nil {
		t.Fatalf("arguments must unmarshal: %v", err)
	}
	if parsed["path"] != "/tmp/a.md" {
		t.Errorf("repair must preserve the model's intent, got %v", parsed["path"])
	}
}

// TestConvertAssistantMessage_ReasoningContentToolCallTurnsOnly pins the DS1
// passback rule (dsh serialize.ts:85-101): reasoning_content is emitted only
// on tool-call turns; plain assistant turns drop it (the API ignores it
// there, so sending it wastes tokens). The compat flag forces the key (empty
// when the turn carries no thinking) on tool-call turns only — DeepSeek's
// thinking mode 400s when a tool-call turn lacks the field.
func TestConvertAssistantMessage_ReasoningContentToolCallTurnsOnly(t *testing.T) {
	toolCallBlock := provider.ContentBlock{
		Type:          provider.ContentBlockToolCall,
		ToolCallID:    "call_1",
		ToolName:      "read",
		ToolArguments: `{"path":"PLAN.md"}`,
	}
	thinkingBlock := provider.ContentBlock{Type: provider.ContentBlockThinking, Thinking: "chain of thought"}
	textBlock := provider.ContentBlock{Type: provider.ContentBlockText, Text: "answer"}

	t.Run("plain turn with thinking drops reasoning_content", func(t *testing.T) {
		msg := provider.NewAssistantMessage([]provider.ContentBlock{thinkingBlock, textBlock})
		got := convertAssistantMessage(msg, provider.OpenAICompletionsCompat{})
		if _, ok := got["reasoning_content"]; ok {
			t.Errorf("plain turn: reasoning_content must be dropped, got %v", got["reasoning_content"])
		}
		if got["content"] != "answer" {
			t.Errorf("expected content answer, got %q", got["content"])
		}
	})

	t.Run("plain turn with thinking and flag on still drops reasoning_content", func(t *testing.T) {
		msg := provider.NewAssistantMessage([]provider.ContentBlock{thinkingBlock, textBlock})
		got := convertAssistantMessage(msg, provider.OpenAICompletionsCompat{
			RequiresReasoningContentOnAssistantMessages: provider.BoolPtr(true),
		})
		if _, ok := got["reasoning_content"]; ok {
			t.Errorf("plain turn: flag must not resurrect reasoning_content, got %v", got["reasoning_content"])
		}
	})

	t.Run("tool-call turn passes thinking back verbatim", func(t *testing.T) {
		msg := provider.NewAssistantMessage([]provider.ContentBlock{thinkingBlock, toolCallBlock})
		got := convertAssistantMessage(msg, provider.OpenAICompletionsCompat{})
		if got["reasoning_content"] != "chain of thought" {
			t.Errorf("tool-call turn: expected reasoning_content passback, got %v", got["reasoning_content"])
		}
	})

	t.Run("tool-call turn without thinking gets forced empty key when flag on", func(t *testing.T) {
		msg := provider.NewAssistantMessage([]provider.ContentBlock{toolCallBlock})
		got := convertAssistantMessage(msg, provider.OpenAICompletionsCompat{
			RequiresReasoningContentOnAssistantMessages: provider.BoolPtr(true),
		})
		rc, ok := got["reasoning_content"]
		if !ok {
			t.Fatal("flag on: tool-call turn must carry the reasoning_content key")
		}
		if rc != "" {
			t.Errorf("expected forced empty reasoning_content, got %v", rc)
		}
	})

	t.Run("tool-call turn without thinking and flag off omits key", func(t *testing.T) {
		msg := provider.NewAssistantMessage([]provider.ContentBlock{toolCallBlock})
		got := convertAssistantMessage(msg, provider.OpenAICompletionsCompat{})
		if _, ok := got["reasoning_content"]; ok {
			t.Errorf("flag off + no thinking: reasoning_content must be absent, got %v", got["reasoning_content"])
		}
	})
}

func TestConvertMessages_ToolCallFollowedByResult(t *testing.T) {
	compat := provider.OpenAICompletionsCompat{ToolResultAsUser: provider.BoolPtr(true)}
	msgs := []provider.Message{
		provider.NewAssistantMessage([]provider.ContentBlock{
			{Type: provider.ContentBlockToolCall, ToolCallID: "call_1", ToolName: "read", ToolArguments: `{"path":"PLAN.md"}`},
			{Type: provider.ContentBlockText, Text: ""},
		}),
		provider.NewToolResultMessage("call_1", "read", "file contents", false),
	}

	got := convertMessages(provider.Model{}, msgs, "", compat)

	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(got), got)
	}
	if got[0]["role"] != "assistant" {
		t.Errorf("expected assistant role, got %q", got[0]["role"])
	}
	toolCalls, ok := got[0]["tool_calls"].([]map[string]interface{})
	if !ok || len(toolCalls) != 1 {
		t.Errorf("expected assistant to have 1 tool_call, got %+v", got[0]["tool_calls"])
	}
	if got[1]["role"] != "user" {
		t.Errorf("expected tool result as user role, got %q", got[1]["role"])
	}
	content, ok := got[1]["content"].(string)
	if !ok || !strings.Contains(content, "call_1") {
		t.Errorf("expected user message to reference call_1, got %q", content)
	}
}

func TestConvertMessages_IncludesExplicitSystemMessage(t *testing.T) {
	compat := provider.OpenAICompletionsCompat{}
	msgs := []provider.Message{
		provider.NewSystemMessage("You are helpful"),
		provider.NewUserMessage("hello"),
	}

	got := convertMessages(provider.Model{}, msgs, "", compat)

	if len(got) != 2 {
		t.Fatalf("expected 2 messages, got %d: %+v", len(got), got)
	}
	if got[0]["role"] != "system" {
		t.Errorf("expected first message role system, got %q", got[0]["role"])
	}
	if got[1]["role"] != "user" {
		t.Errorf("expected second message role user, got %q", got[1]["role"])
	}
}

func TestConvertMessages_UserWithImageEmitsImageUrl(t *testing.T) {
	compat := provider.OpenAICompletionsCompat{}
	msgs := []provider.Message{
		provider.NewUserMessageWithImage("describe this", "data:image/png;base64,ABC"),
	}

	got := convertMessages(provider.Model{}, msgs, "", compat)

	if len(got) != 1 {
		t.Fatalf("expected 1 message, got %d: %+v", len(got), got)
	}
	if got[0]["role"] != "user" {
		t.Errorf("expected user role, got %q", got[0]["role"])
	}
	parts, ok := got[0]["content"].([]map[string]interface{})
	if !ok || len(parts) != 2 {
		t.Fatalf("expected 2 content parts, got %+v", got[0]["content"])
	}
	if parts[0]["type"] != "text" || parts[0]["text"] != "describe this" {
		t.Errorf("first part = %+v, want text 'describe this'", parts[0])
	}
	if parts[1]["type"] != "image_url" {
		t.Errorf("second part type = %v, want image_url", parts[1]["type"])
	}
	imageURL, ok := parts[1]["image_url"].(map[string]interface{})
	if !ok || imageURL["url"] != "data:image/png;base64,ABC" {
		t.Errorf("image_url = %+v, want data:image/png;base64,ABC", imageURL)
	}
}
