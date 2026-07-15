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

func sseBody(lines ...string) io.Reader {
	return strings.NewReader(strings.Join(lines, "\n\n") + "\n\n")
}

func TestOpenAICompletionsParseText(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`data: {"choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
		`data: {"choices":[{"index":0,"delta":{"content":" world"},"finish_reason":"stop"}]}`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	assert.Equal(t, schema.StopReasonEndTurn, result.StopReason)
	assert.Equal(t, "Hello world", textFromResult(result))
}

func TestOpenAICompletionsParseReasoning(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`data: {"choices":[{"index":0,"delta":{"reasoning_content":"I think"}}]}`,
		`data: {"choices":[{"index":0,"delta":{"reasoning_content":"..."},"finish_reason":"stop"}]}`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	assert.Equal(t, schema.ContentBlockThinking, result.Content[0].Type)
	assert.Equal(t, "I think...", result.Content[0].Thinking)
}

func TestOpenAICompletionsParseToolCall(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"weather"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"{\"city\":\"\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"\"NYC\"}"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	assert.Equal(t, schema.ContentBlockToolCall, result.Content[0].Type)
	assert.Equal(t, "call_1", result.Content[0].ToolCallID)
	assert.Equal(t, "weather", result.Content[0].ToolName)
	assert.Contains(t, result.Content[0].ToolArguments, "NYC")
}

func TestOpenAICompletionsParseToolCallStreamsStartAndDelta(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(16)
	go p.ParseResponse(sseBody(
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"write","arguments":""}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"a"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"b"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"function":{"arguments":"c"}}]}}]}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"tool_calls"}]}`,
	), stream)

	var start, delta, end int
	for ev := range stream.Seq() {
		switch ev.Type {
		case schema.EventToolCallStart:
			start++
			require.Len(t, ev.Partial.Content, 1)
			assert.Equal(t, "write", ev.Partial.Content[0].ToolName)
		case schema.EventToolCallDelta:
			delta++
			require.Len(t, ev.Partial.Content, 1)
			assert.Equal(t, "write", ev.Partial.Content[0].ToolName)
		case schema.EventToolCallEnd:
			end++
		}
	}
	assert.Equal(t, 1, start, "expected one EventToolCallStart")
	assert.Equal(t, 3, delta, "expected three EventToolCallDelta events")
	assert.Equal(t, 1, end, "expected one EventToolCallEnd")
}

func TestOpenAICompletionsParseUsageAndCache(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`data: {"choices":[{"index":0,"delta":{"content":"hi"}}]}`,
		`data: {"usage":{"prompt_tokens":10,"completion_tokens":5,"prompt_tokens_details":{"cached_tokens":2,"cache_write_tokens":1}}}`,
		`data: {"choices":[{"index":0,"delta":{},"finish_reason":"stop"}]}`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	require.NotNil(t, result.Usage)
	assert.Equal(t, 7, result.Usage.InputTokens) // 10 - 2 - 1
	assert.Equal(t, 5, result.Usage.OutputTokens)
	assert.Equal(t, 2, result.Usage.CacheReadTokens)
	assert.Equal(t, 1, result.Usage.CacheCreationTokens)
}

func TestOpenAICompletionsParseDoneMarker(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`data: {"choices":[{"index":0,"delta":{"content":"Hello"}}]}`,
		`data: [DONE]`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	assert.Equal(t, "Hello", textFromResult(result))
}

func textFromResult(result *schema.AssistantMessage) string {
	var text string
	for _, b := range result.Content {
		if b.Type == schema.ContentBlockText {
			text += b.Text
		}
	}
	return text
}
