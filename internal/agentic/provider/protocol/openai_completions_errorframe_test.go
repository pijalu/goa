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
