// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"io"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func anthropicSSE(lines ...string) io.Reader {
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(line)
		b.WriteString("\n")
	}
	return strings.NewReader(b.String())
}

func TestAnthropicParseText(t *testing.T) {
	p := ForAPI(schema.ApiAnthropicMessages)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(16)
	go p.ParseResponse(anthropicSSE(
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg-1","usage":{"input_tokens":3,"output_tokens":0}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hello"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":" world"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	assert.Equal(t, schema.StopReasonEndTurn, result.StopReason)
	assert.Equal(t, "Hello world", textFromResult(result))
}

func TestAnthropicParseThinkingAndToolUse(t *testing.T) {
	p := ForAPI(schema.ApiAnthropicMessages)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(16)
	go p.ParseResponse(anthropicSSE(
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg-2","usage":{"input_tokens":5,"output_tokens":0}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"thinking","thinking":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"thinking_delta","thinking":"I think"}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"signature_delta","signature":"sig-1"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":1,"content_block":{"type":"tool_use","id":"toolu_1","name":"weather","input":{}}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":1,"delta":{"type":"input_json_delta","partial_json":"{\"city\":\"NYC\"}"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":1}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"tool_use"},"usage":{"output_tokens":4}}`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	require.Len(t, result.Content, 2)
	assert.Equal(t, schema.ContentBlockThinking, result.Content[0].Type)
	assert.Equal(t, "I think", result.Content[0].Thinking)
	assert.Equal(t, "sig-1", result.Content[0].ThinkingSignature)
	assert.Equal(t, schema.ContentBlockToolCall, result.Content[1].Type)
	assert.Equal(t, "toolu_1", result.Content[1].ToolCallID)
	assert.Equal(t, "weather", result.Content[1].ToolName)
	assert.Contains(t, result.Content[1].ToolArguments, "NYC")
}

func TestAnthropicParseError(t *testing.T) {
	p := ForAPI(schema.ApiAnthropicMessages)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(4)
	go p.ParseResponse(anthropicSSE(
		"event: error",
		`data: {"type":"error","error":{"type":"rate_limit_error","message":"too many requests"}}`,
	), stream)

	require.Error(t, stream.Err())
}
