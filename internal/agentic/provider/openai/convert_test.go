// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package openai

import (
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
