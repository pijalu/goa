// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"io"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/hooks"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func googleSSE(lines ...string) io.Reader {
	return strings.NewReader(strings.Join(lines, "\n\n") + "\n\n")
}

// A mid-stream Google error frame ({"error":{"code":503,...}}) must
// surface as a classified, retryable *hooks.ProviderError instead of being
// silently skipped (zero candidates) — which ended the stream "cleanly"
// and misreported the failure as an empty response.
func TestGoogleParseErrorFrame5xxIsRetryable(t *testing.T) {
	p := ForAPI(schema.ApiGoogleGenerativeAI)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(googleSSE(
		`data: {"error":{"code":503,"message":"The model is overloaded. Please try again later.","status":"UNAVAILABLE"}}`,
	), stream)

	err := stream.Err()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "overloaded")
	var provErr *hooks.ProviderError
	require.ErrorAs(t, err, &provErr, "error frame must be classified, not a bare error")
	assert.Equal(t, 503, provErr.StatusCode())
	assert.True(t, provErr.IsRetryable, "mid-stream 503 must be retryable")
}

// A 4xx Google error frame stays non-retryable.
func TestGoogleParseErrorFrame400NotRetryable(t *testing.T) {
	p := ForAPI(schema.ApiGoogleGenerativeAI)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(googleSSE(
		`data: {"error":{"code":400,"message":"Invalid JSON payload received.","status":"INVALID_ARGUMENT"}}`,
	), stream)

	err := stream.Err()
	require.Error(t, err)
	var provErr *hooks.ProviderError
	require.ErrorAs(t, err, &provErr)
	assert.Equal(t, 400, provErr.StatusCode())
	assert.False(t, provErr.IsRetryable)
}

func TestGoogleParseText(t *testing.T) {
	p := ForAPI(schema.ApiGoogleGenerativeAI)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(googleSSE(
		`data: {"candidates":[{"content":{"parts":[{"text":"Hello"}]}}]}`,
		`data: {"candidates":[{"content":{"parts":[{"text":" world"}]},"finishReason":"STOP"}]}`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	assert.Equal(t, schema.StopReasonEndTurn, result.StopReason)
	assert.Equal(t, "Hello world", textFromResult(result))
}

func TestGoogleParseFunctionCall(t *testing.T) {
	p := ForAPI(schema.ApiGoogleGenerativeAI)
	require.NotNil(t, p)

	stream := schema.NewAssistantMessageEventStream(8)
	go p.ParseResponse(googleSSE(
		`data: {"candidates":[{"content":{"parts":[{"functionCall":{"name":"weather","args":{"city":"NYC"}}}]}}]}`,
		`data: {"candidates":[{"finishReason":"STOP"}]}`,
	), stream)

	result := stream.Result()
	require.NotNil(t, result)
	require.Len(t, result.Content, 1)
	assert.Equal(t, schema.ContentBlockToolCall, result.Content[0].Type)
	assert.Equal(t, "weather", result.Content[0].ToolName)
	assert.Contains(t, result.Content[0].ToolArguments, "NYC")
}
