// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The agent's window-edge overflow guard keys off finish_reason=length. The
// parser must not flatten it to EndTurn (it historically flattened every
// reason, hiding the truncation that precedes a context_length_exceeded).
func TestOpenAICompletionsFinishReasonLengthMapsToMaxTokens(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`data: {"choices":[{"index":0,"delta":{"content":"truncated mid-sen"},"finish_reason":"length"}]}`,
		`data: {"choices":[],"usage":{"prompt_tokens":9950,"completion_tokens":40,"total_tokens":9990}}`,
		`data: [DONE]`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	assert.Equal(t, schema.StopReasonMaxTokens, result.StopReason)
}

func TestOpenAICompletionsFinishReasonMapping(t *testing.T) {
	cases := map[string]schema.StopReason{
		"stop":           schema.StopReasonEndTurn,
		"tool_calls":     schema.StopReasonToolCall,
		"function_call":  schema.StopReasonToolCall,
		"content_filter": schema.StopReasonContentFiltered,
		"":               schema.StopReasonEndTurn,
		"some-new-value": schema.StopReasonEndTurn, // unknown degrades safely
	}
	for reason, want := range cases {
		assert.Equalf(t, want, mapOpenAIFinishReason(reason), "mapOpenAIFinishReason(%q)", reason)
	}
}

func TestOpenAICompletionsFinishReasonToolCallsSurfaces(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"call_1","type":"function","function":{"name":"bash","arguments":"{}"}}]},"finish_reason":"tool_calls"}]}`,
		`data: [DONE]`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	assert.Equal(t, schema.StopReasonToolCall, result.StopReason)
}
