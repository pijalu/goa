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

// --- Codex request body shape ------------------------------------------------

// TestCodexBodyShape pins the codex subscription request contract (mirrors
// Pi's buildRequestBody): system prompt in the instructions field (not an
// input message), store=false (the subscription rejects store=true with
// "Store must be set to false"), tool_choice auto + parallel tool calls.
func TestCodexBodyShape(t *testing.T) {
	model := schema.Model{
		ID: "gpt-5.6-luna", Api: schema.ApiOpenAICodexResponses,
		Provider: schema.ProviderOpenAICodex, Reasoning: true,
	}
	ctx := schema.Context{
		SystemPrompt: "You are a coding agent.",
		Messages: []schema.Message{
			{Role: schema.RoleUser, Content: []schema.ContentBlock{{Type: schema.ContentBlockText, Text: "hi"}}},
		},
		Tools: []schema.ToolSchema{{Name: "read", Description: "read a file", InputSchema: map[string]any{"type": "object"}}},
	}
	profile := schema.ResolveProfile(model)
	require.Equal(t, "openai-codex-responses", profile.ID, "codex profile must resolve")

	body, err := buildResponsesBody(model, ctx, schema.StreamOptions{}, profile, "codex")
	require.NoError(t, err)

	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m))

	assert.Equal(t, "You are a coding agent.", m["instructions"], "codex system prompt goes to instructions")
	assert.Equal(t, false, m["store"], "codex subscription requires store=false")
	assert.Equal(t, "auto", m["tool_choice"])
	assert.Equal(t, true, m["parallel_tool_calls"])

	// The system prompt must NOT also appear as a leading input message.
	input, ok := m["input"].([]any)
	require.True(t, ok, "input must be an array")
	for _, item := range input {
		msg, _ := item.(map[string]any)
		assert.NotEqual(t, "system", msg["role"], "codex system prompt must not be an input message")
	}
}

// TestCodexBodyNoToolsCollapse ensures the final-step text-only collapse
// overrides codex's tool_choice:auto and drops parallel_tool_calls.
func TestCodexBodyNoToolsCollapse(t *testing.T) {
	model := schema.Model{ID: "gpt-5.6-luna", Api: schema.ApiOpenAICodexResponses, Provider: schema.ProviderOpenAICodex}
	ctx := schema.Context{SystemPrompt: "s", NoTools: true}
	profile := schema.ResolveProfile(model)

	body, err := buildResponsesBody(model, ctx, schema.StreamOptions{}, profile, "codex")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m))

	assert.Equal(t, "none", m["tool_choice"])
	_, hasParallel := m["parallel_tool_calls"]
	assert.False(t, hasParallel, "NoTools collapse must drop parallel_tool_calls")
}

// --- Codex history serialization (responses output items) --------------------

// TestResponsesToolResultSerialization pins the responses-API history shape:
// tool results become function_call_output items keyed by call_id (the
// chat-completions role:"tool" message is rejected by the responses backend).
func TestResponsesToolResultSerialization(t *testing.T) {
	model := schema.Model{ID: "gpt-5.6-luna", Api: schema.ApiOpenAICodexResponses, Provider: schema.ProviderOpenAICodex}
	profile := schema.ResolveProfile(model)
	ctx := schema.Context{
		Messages: []schema.Message{
			{Role: schema.RoleUser, Content: []schema.ContentBlock{{Type: schema.ContentBlockText, Text: "weather?"}}},
			{Role: schema.RoleAssistant, Content: []schema.ContentBlock{{
				Type: schema.ContentBlockToolCall, ToolCallID: "call_abc", ToolName: "get_weather", ToolArguments: `{"city":"Paris"}`,
			}}},
			{Role: schema.RoleToolResult, Content: []schema.ContentBlock{{
				Type: schema.ContentBlockToolResult, ToolCallID: "call_abc", ToolName: "get_weather", Text: `{"temp":"22C"}`,
			}}},
		},
	}

	body, err := buildResponsesBody(model, ctx, schema.StreamOptions{}, profile, "codex")
	require.NoError(t, err)
	var m map[string]any
	require.NoError(t, json.Unmarshal(body, &m))

	input, ok := m["input"].([]any)
	require.True(t, ok)
	// Expect: user message, function_call item, function_call_output item.
	var types []string
	var callOutput map[string]any
	for _, item := range input {
		msg, _ := item.(map[string]any)
		if ty, _ := msg["type"].(string); ty != "" {
			types = append(types, ty)
			if ty == "function_call_output" {
				callOutput = msg
			}
		} else if role, _ := msg["role"].(string); role != "" {
			types = append(types, "role:"+role)
		}
	}
	assert.Contains(t, types, "function_call", "assistant tool call must serialize as function_call")
	assert.Contains(t, types, "function_call_output", "tool result must serialize as function_call_output")
	require.NotNil(t, callOutput)
	assert.Equal(t, "call_abc", callOutput["call_id"])
	assert.Equal(t, `{"temp":"22C"}`, callOutput["output"])
	assert.NotContains(t, types, "role:tool", "role:tool is not valid for the responses API")
}

// --- Codex SSE tool-call argument streaming ----------------------------------

// TestCodexParseToolCallArgumentStreaming pins the fix for streamed tool-call
// arguments: the responses API streams arguments via function_call_arguments
// delta/done and finalizes the call in output_item.done. Prior to the fix the
// parser only read output_item.added (no arguments), dropping them entirely.
func TestCodexParseToolCallArgumentStreaming(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICodexResponses)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":""}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"city\":"}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"\"Paris\"}"}`,
		`data: {"type":"response.function_call_arguments.done","output_index":0,"arguments":"{\"city\":\"Paris\"}"}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_1","name":"get_weather","arguments":"{\"city\":\"Paris\"}"}}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":10,"output_tokens":5}}}`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	require.NoError(t, stream.Err())
	require.Len(t, result.Content, 1, "expected exactly one tool call")
	tc := result.Content[0]
	assert.Equal(t, schema.ContentBlockToolCall, tc.Type)
	assert.Equal(t, "call_1", tc.ToolCallID, "tool result matching uses call_id")
	assert.Equal(t, "get_weather", tc.ToolName)
	assert.JSONEq(t, `{"city":"Paris"}`, tc.ToolArguments, "streamed arguments must accumulate")
}

// TestCodexParseToolCallInlineArguments covers backends that send the full
// arguments inline on output_item.done with no streaming delta events.
func TestCodexParseToolCallInlineArguments(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICodexResponses)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_1","call_id":"call_9","name":"read","arguments":"{\"path\":\"/tmp/x\"}"}}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	assert.Equal(t, "call_9", result.Content[0].ToolCallID)
	assert.JSONEq(t, `{"path":"/tmp/x"}`, result.Content[0].ToolArguments)
}

// TestCodexParseMultipleToolCalls ensures concurrent function_call output
// items accumulate arguments independently (keyed by output_index).
func TestCodexParseMultipleToolCalls(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICodexResponses)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`data: {"type":"response.output_item.added","output_index":0,"item":{"type":"function_call","id":"fc_a","call_id":"call_a","name":"read","arguments":""}}`,
		`data: {"type":"response.output_item.added","output_index":1,"item":{"type":"function_call","id":"fc_b","call_id":"call_b","name":"write","arguments":""}}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":1,"delta":"{\"p\":1}"}`,
		`data: {"type":"response.function_call_arguments.delta","output_index":0,"delta":"{\"q\":2}"}`,
		`data: {"type":"response.output_item.done","output_index":0,"item":{"type":"function_call","id":"fc_a","call_id":"call_a","name":"read","arguments":"{\"q\":2}"}}`,
		`data: {"type":"response.output_item.done","output_index":1,"item":{"type":"function_call","id":"fc_b","call_id":"call_b","name":"write","arguments":"{\"p\":1}"}}`,
		`data: {"type":"response.completed","response":{"status":"completed","usage":{"input_tokens":1,"output_tokens":1}}}`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	require.Len(t, result.Content, 2, "both tool calls must be emitted")
	byID := map[string]string{}
	for _, c := range result.Content {
		byID[c.ToolCallID] = c.ToolArguments
	}
	assert.JSONEq(t, `{"q":2}`, byID["call_a"])
	assert.JSONEq(t, `{"p":1}`, byID["call_b"])
}
