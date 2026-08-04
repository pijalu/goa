// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/hooks"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression (2026-08-04 LM Studio export): the server rejected the request
// with HTTP 400 ("System message must be at the beginning") but delivered it
// as an HTTP 200 SSE "event: error" frame. The parser ignored the payload,
// the stream ended "cleanly" with zero events, and the agent misreported it
// as "provider returned an empty response", retrying a payload that could
// never succeed and bricking the session. The error frame must surface as a
// stream error carrying the provider's message.
func TestOpenAICompletionsErrorFrameSurfacesStreamError(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`event: error`+"\n"+`data: {"error":{"message":"Engine protocol predict request returned 400: System message must be at the beginning.","type":"invalid_request_error"}}`,
	), stream)

	err := stream.Err()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "System message must be at the beginning")
}

// A normal stream after the fix: error-frame detection must not break the
// regular chunk flow (no "error" key present).
func TestOpenAICompletionsErrorFrameAbsentOnNormalStream(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`data: {"choices":[{"index":0,"delta":{"content":"Hello"},"finish_reason":"stop"}]}`,
		`data: [DONE]`,
	), stream)

	require.NoError(t, stream.Err())
	result := stream.Result()
	require.NotNil(t, result)
}

func TestDetectOpenAIChunkError(t *testing.T) {
	cases := []struct {
		name    string
		chunk   string
		wantErr string // empty means no error expected
	}{
		{
			name:    "error object with message",
			chunk:   `{"error":{"message":"boom"}}`,
			wantErr: "LLM error: boom",
		},
		{
			name:    "nested message wins over outer message",
			chunk:   `{"message":"outer","error":{"message":"inner"}}`,
			wantErr: "LLM error: inner",
		},
		{
			name:    "outer message used when nested missing",
			chunk:   `{"message":"outer only","error":{"type":"server_error"}}`,
			wantErr: "LLM error: outer only",
		},
		{
			name:    "string error degrades to generic",
			chunk:   `{"error":"rate limited"}`,
			wantErr: "LLM error: provider error",
		},
		{
			name:    "null error is not an error",
			chunk:   `{"error":null,"choices":[]}`,
			wantErr: "",
		},
		{
			name:    "choices chunk without error key",
			chunk:   `{"choices":[{"index":0,"delta":{"content":"hi"}}]}`,
			wantErr: "",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := parseOpenAIChunk(c.chunk)
			if c.wantErr == "" {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			assert.Equal(t, c.wantErr, err.Error())
		})
	}
}

// Regression (2026-08-21 llama.cpp/LM Studio): a mid-stream failure was
// delivered as an HTTP 200 error frame whose message embeds the upstream
// status — {"error":{"message":"Streaming response failed: [503] The
// request queue is full."}}. The frame bypasses the transport's error hook
// (HTTP 200), so it reached the agent as a bare error: non-retryable, the
// turn died with zero retries and goal mode paused. Every 5xx frame must
// surface as a classified, retryable *hooks.ProviderError.
func TestOpenAICompletionsErrorFrame5xxIsRetryable(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`event: error`+"\n"+`data: {"error":{"message":"Streaming response failed: [503] The request queue is full.","type":"server_error"}}`,
	), stream)

	err := stream.Err()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "The request queue is full")

	var provErr *hooks.ProviderError
	require.ErrorAs(t, err, &provErr, "error frame must be classified, not a bare error")
	assert.Equal(t, 503, provErr.StatusCode())
	assert.True(t, provErr.IsRetryable, "mid-stream 503 must be retryable")
	assert.False(t, provErr.IsContextOverflow, "queue-full must not be misread as context overflow")
	assert.False(t, provErr.IsRateLimit)
}

// A 429 error frame must be retryable AND flagged as a rate limit so the
// backoff path treats it consistently with an HTTP-level 429.
func TestOpenAICompletionsErrorFrame429IsRateLimited(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`event: error`+"\n"+`data: {"error":{"message":"Rate limit reached","type":"rate_limit_error","code":"429"}}`,
	), stream)

	err := stream.Err()
	require.Error(t, err)
	var provErr *hooks.ProviderError
	require.ErrorAs(t, err, &provErr)
	assert.Equal(t, 429, provErr.StatusCode())
	assert.True(t, provErr.IsRateLimit)
	assert.True(t, provErr.IsRetryable)
}

// A 400 error frame (chat-template rejection) stays non-retryable: retrying
// a malformed request cannot succeed and must not burn the retry budget.
func TestOpenAICompletionsErrorFrame400NotRetryable(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`event: error`+"\n"+`data: {"error":{"message":"Engine protocol predict request returned 400: System message must be at the beginning.","type":"invalid_request_error"}}`,
	), stream)

	err := stream.Err()
	require.Error(t, err)
	var provErr *hooks.ProviderError
	require.ErrorAs(t, err, &provErr)
	assert.Equal(t, 400, provErr.StatusCode())
	assert.False(t, provErr.IsRetryable, "mid-stream 400 must not be retried")
}

// A mid-stream context-overflow frame must carry the overflow flag so the
// agent picks the compress+retry path instead of a plain bounded retry.
func TestOpenAICompletionsErrorFrameContextOverflow(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody(
		`event: error`+"\n"+`data: {"error":{"message":"This model's maximum context length is 4096 tokens.","type":"invalid_request_error","code":"context_length_exceeded"}}`,
	), stream)

	err := stream.Err()
	require.Error(t, err)
	var provErr *hooks.ProviderError
	require.ErrorAs(t, err, &provErr)
	assert.True(t, provErr.IsContextOverflow)
	assert.True(t, provErr.IsRetryable)
}
