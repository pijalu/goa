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

// Regression (2026-08-24 export): OpenRouter's upstream provider died
// mid-generation and the router masked it as a clean completion — HTTP 200,
// finish_reason="stop", zero content, with only native_finish_reason=
// "network_error" revealing the failure. The parser honored the flattened
// finish_reason: the turn ended cleanly with no message and no retry, and
// the session went silent right after a tool call. An error-marked
// native_finish_reason must surface as a retryable stream error (502:
// gateway answered 200 while its upstream hop failed).
func TestOpenAICompletionsNativeFinishNetworkErrorIsRetryable(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	// Byte-exact final chunk from the captured export (http.jsonl tail).
	chunk := `{"id":"gen-1787584664-eWWONm1t8n4HvjoelZt8n","object":"chat.completion.chunk","created":1787584664,"model":"stealth/ox-alpha","provider":"Stealth","choices":[{"index":0,"delta":{"content":"","role":"assistant"},"finish_reason":"stop","native_finish_reason":"network_error"}]}`

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(sseBody("data: "+chunk, "data: [DONE]"), stream)

	err := stream.Err()
	require.Error(t, err, "masked network error must not end the stream cleanly")
	assert.Contains(t, err.Error(), "native_finish_reason")

	var provErr *hooks.ProviderError
	require.ErrorAs(t, err, &provErr, "must be classified, not a bare error")
	assert.Equal(t, 502, provErr.StatusCode())
	assert.True(t, provErr.IsRetryable, "upstream network failure is transient — must be retried")
	assert.False(t, provErr.IsRateLimit)
	assert.False(t, provErr.IsContextOverflow)

	// The masked stop must NOT produce a finalized empty assistant message.
	assert.Nil(t, stream.Result(), "no result may be synthesized from a failed stream")
}

// A normal OpenRouter chunk carrying a non-error native_finish_reason must
// keep flowing exactly as before — the check only fires on error-marked
// values.
func TestOpenAICompletionsNativeFinishNormalReasonsUnaffected(t *testing.T) {
	cases := []struct {
		name         string
		native       string
		finishReason string
	}{
		{"stop passthrough", "stop", "stop"},
		{"provider-specific tool_calls", "tool_calls", "tool_calls"},
		{"length passthrough", "length", "length"},
		{"unknown provider reason", "eos", "stop"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			chunk := `{"choices":[{"index":0,"delta":{"content":"hi"},"finish_reason":"` + c.finishReason + `","native_finish_reason":"` + c.native + `"}]}`
			msgs, err := parseOpenAIChunk(chunk)
			require.NoError(t, err)
			require.NotEmpty(t, msgs, "chunk must still parse to messages")
		})
	}
}
