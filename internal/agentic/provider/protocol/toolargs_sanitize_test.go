// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"encoding/json"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// malformedToolCallMessage builds an assistant message carrying a tool call
// whose arguments were truncated mid-stream (the poolside failure mode: the
// provider ended the stream with finish_reason "tool_calls" before the
// closing brace).
func malformedToolCallMessage() schema.Message {
	return schema.Message{
		Role: schema.RoleAssistant,
		Content: []schema.ContentBlock{{
			Type:          schema.ContentBlockToolCall,
			ToolCallID:    "chatcmpl-tool-06aa7ecf",
			ToolName:      "edit",
			ToolArguments: `{"path": "/tmp/a.md", "old_string": "a", "new_string": "b"`,
		}},
	}
}

// TestConvertAssistantMessage_SanitizesMalformedToolArguments is the
// regression test for the poolside 400 "Invalid JSON in tool call arguments":
// a truncated tool call in history must be re-serialized as valid JSON or the
// provider rejects the whole request and poisons the session.
func TestConvertAssistantMessage_SanitizesMalformedToolArguments(t *testing.T) {
	out := convertAssistantMessage(malformedToolCallMessage(), openAICompletionsCompat{})

	toolCalls, ok := out["tool_calls"].([]map[string]any)
	require.True(t, ok, "assistant message must carry tool_calls")
	require.Len(t, toolCalls, 1)
	fn, ok := toolCalls[0]["function"].(map[string]any)
	require.True(t, ok)
	args, ok := fn["arguments"].(string)
	require.True(t, ok)

	assert.True(t, json.Valid([]byte(args)), "arguments must be valid JSON, got %q", args)
	var parsed map[string]any
	require.NoError(t, json.Unmarshal([]byte(args), &parsed))
	assert.Equal(t, "/tmp/a.md", parsed["path"], "repair must preserve the model's intent")
	assert.Equal(t, "b", parsed["new_string"])
}

// TestConvertAssistantMessage_PreservesValidToolArguments pins the no-op
// path: well-formed arguments serialize byte-identical.
func TestConvertAssistantMessage_PreservesValidToolArguments(t *testing.T) {
	valid := `{"path":"/tmp/a.md","old_string":"a","new_string":"b"}`
	msg := schema.Message{
		Role: schema.RoleAssistant,
		Content: []schema.ContentBlock{{
			Type:          schema.ContentBlockToolCall,
			ToolCallID:    "call_1",
			ToolName:      "edit",
			ToolArguments: valid,
		}},
	}
	out := convertAssistantMessage(msg, openAICompletionsCompat{})
	toolCalls := out["tool_calls"].([]map[string]any)
	fn := toolCalls[0]["function"].(map[string]any)
	assert.Equal(t, valid, fn["arguments"])
}

// TestBuildRequest_MalformedHistoricalToolCallDoesNotPoisonRequest exercises
// the full request-build path with a poisoned history: assistant message with
// truncated tool call arguments followed by its (error) tool result. The
// marshaled request must carry valid-JSON arguments so strict providers
// accept it.
func TestBuildRequest_MalformedHistoricalToolCallDoesNotPoisonRequest(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	ctx := schema.Context{
		Messages: []schema.Message{
			schema.NewUserMessage("update the plan"),
			malformedToolCallMessage(),
			schema.NewToolResultMessage("chatcmpl-tool-06aa7ecf", "edit",
				"Error: [edit error: invalid_input]\nCannot parse parameters: unexpected end of JSON input", true),
		},
	}
	body, err := p.BuildRequest(
		schema.Model{ID: "poolside/laguna-s-2.1", Api: schema.ApiOpenAICompletions},
		ctx, schema.StreamOptions{MaxTokens: 1024}, schema.VariantProfile{})
	require.NoError(t, err)

	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	messages, ok := req["messages"].([]any)
	require.True(t, ok)
	for i, m := range messages {
		mm, ok := m.(map[string]any)
		require.True(t, ok)
		tcs, ok := mm["tool_calls"].([]any)
		if !ok {
			continue
		}
		for j, tc := range tcs {
			fn := tc.(map[string]any)["function"].(map[string]any)
			args := fn["arguments"].(string)
			assert.True(t, json.Valid([]byte(args)),
				"messages[%d].tool_calls[%d] arguments must be valid JSON, got %q", i, j, args)
		}
	}
}

// TestConvertAnthropicAssistantBlocks_SanitizesMalformedToolArguments covers
// the RawMessage serialization path: malformed arguments previously failed
// the whole request marshal; they must now degrade to valid JSON.
func TestConvertAnthropicAssistantBlocks_SanitizesMalformedToolArguments(t *testing.T) {
	blocks := convertAnthropicAssistantBlocks(malformedToolCallMessage().Content)
	require.Len(t, blocks, 1)

	_, err := json.Marshal(map[string]any{"role": "assistant", "content": blocks})
	require.NoError(t, err, "malformed tool input must not break request marshal")

	input, ok := blocks[0]["input"].(json.RawMessage)
	require.True(t, ok)
	assert.True(t, json.Valid(input), "tool_use input must be valid JSON, got %q", string(input))
}

// TestConvertGoogleParts_SanitizesMalformedToolArguments covers the Google
// functionCall args RawMessage path.
func TestConvertGoogleParts_SanitizesMalformedToolArguments(t *testing.T) {
	parts := convertGoogleParts(malformedToolCallMessage().Content, schema.RoleAssistant)
	require.Len(t, parts, 1)

	_, err := json.Marshal(map[string]any{"role": "model", "parts": parts})
	require.NoError(t, err, "malformed functionCall args must not break request marshal")

	fc, ok := parts[0]["functionCall"].(map[string]any)
	require.True(t, ok)
	args, ok := fc["args"].(json.RawMessage)
	require.True(t, ok)
	assert.True(t, json.Valid(args), "functionCall args must be valid JSON, got %q", string(args))
}
