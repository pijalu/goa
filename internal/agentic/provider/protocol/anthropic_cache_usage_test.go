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

// Anthropic returns cache accounting on the message_start usage payload
// (cache_creation_input_tokens / cache_read_input_tokens) and the cumulative
// output token count on message_delta. The stream result must surface all
// four buckets so the app can compute cache-aware cost and the footer cache
// hit-rate. Regression test for the bug where only input/output were parsed
// and the cache buckets were silently dropped (always zero for Anthropic).
func TestAnthropicParseCacheUsage(t *testing.T) {
	p := ForAPI(schema.ApiAnthropicMessages)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(16)
	go p.ParseResponse(anthropicSSE(
		"event: message_start",
		`data: {"type":"message_start","message":{"id":"msg-cache","usage":{"input_tokens":12,"output_tokens":0,"cache_creation_input_tokens":1500,"cache_read_input_tokens":8000}}}`,
		"",
		"event: content_block_start",
		`data: {"type":"content_block_start","index":0,"content_block":{"type":"text","text":""}}`,
		"",
		"event: content_block_delta",
		`data: {"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"Hi"}}`,
		"",
		"event: content_block_stop",
		`data: {"type":"content_block_stop","index":0}`,
		"",
		"event: message_delta",
		`data: {"type":"message_delta","delta":{"stop_reason":"end_turn"},"usage":{"output_tokens":2}}`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	require.NotNil(t, result.Usage, "usage must be populated from the stream")

	assert.Equal(t, 12, result.Usage.InputTokens, "fresh input tokens")
	assert.Equal(t, 2, result.Usage.OutputTokens, "output tokens from message_delta")
	assert.Equal(t, 1500, result.Usage.CacheCreationTokens, "cache write tokens from message_start")
	assert.Equal(t, 8000, result.Usage.CacheReadTokens, "cache read tokens from message_start")
}
