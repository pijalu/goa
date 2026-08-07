// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewStreamFrameErrorClassification(t *testing.T) {
	cases := []struct {
		name         string
		msg          string
		status       int
		wantRetry    bool
		wantRateLim  bool
		wantOverflow bool
	}{
		// Every 5xx is intrinsically retryable — server-side/transient by
		// definition. Regression: a mid-stream "[503] The request queue is
		// full." frame surfaced as a fatal error and paused goal mode with
		// zero retries.
		{"500 internal", "Internal Server Error", 500, true, false, false},
		{"502 bad gateway", "Bad Gateway", 502, true, false, false},
		{"503 queue full", "Streaming response failed: [503] The request queue is full.", 503, true, false, false},
		{"504 gateway timeout", "Gateway Timeout", 504, true, false, false},
		{"529 overloaded", "Overloaded", 529, true, false, false},
		{"599 upper bound", "unknown server error", 599, true, false, false},
		// 408/429 are retryable like the HTTP-level classification.
		{"408 request timeout", "Request Timeout", 408, true, false, false},
		{"429 rate limit", "Too Many Requests", 429, true, true, false},
		// Other 4xx stay non-retryable: a malformed request cannot succeed
		// on retry and must not burn the retry budget.
		{"400 bad request", "System message must be at the beginning", 400, false, false, false},
		{"401 unauthorized", "invalid api key", 401, false, false, false},
		{"403 forbidden", "forbidden", 403, false, false, false},
		{"404 not found", "model not found", 404, false, false, false},
		{"unknown status", "boom", 0, false, false, false},
		// Text-pattern enrichment without a numeric status: the agent needs
		// the overflow flag to pick the compress+retry path, and rate-limit
		// frames without codes still deserve a retry.
		{"overflow text no status", "context length exceeded", 0, true, false, true},
		{"overflow text with 400", "This model's maximum context length is 4096 tokens", 400, true, false, true},
		{"rate limit text no status", "rate limit reached, slow down", 0, true, true, false},
		// Non-overflow suppression: Bedrock-style throttling must not be
		// misclassified as context overflow.
		{"throttling suppresses overflow", "Throttling error: Too many tokens", 0, false, false, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pe := NewStreamFrameError(errors.New(tc.msg), tc.status, "")
			require.NotNil(t, pe)
			assert.Equal(t, tc.wantRetry, pe.IsRetryable, "IsRetryable")
			assert.Equal(t, tc.wantRateLim, pe.IsRateLimit, "IsRateLimit")
			assert.Equal(t, tc.wantOverflow, pe.IsContextOverflow, "IsContextOverflow")
			assert.Equal(t, tc.status, pe.StatusCode(), "StatusCode")
			assert.Equal(t, tc.msg, pe.Error(), "message text must be preserved")
		})
	}
}

// The error frame must NOT be misclassified as overflow just because the
// body carries a token count: "queue is full" has no overflow semantics.
func TestNewStreamFrameErrorQueueFullIsNotOverflow(t *testing.T) {
	pe := NewStreamFrameError(errors.New("LLM error: Streaming response failed: [503] The request queue is full."), 503,
		`{"error":{"message":"Streaming response failed: [503] The request queue is full."}}`)
	require.NotNil(t, pe)
	assert.True(t, pe.IsRetryable)
	assert.False(t, pe.IsContextOverflow)
	assert.Equal(t, 503, pe.StatusCode())
	// The raw frame rides along so the UI can decode a rich retry bubble.
	assert.Contains(t, pe.ResponseBody(), "queue is full")
}

func TestNewStreamFrameErrorNil(t *testing.T) {
	assert.Nil(t, NewStreamFrameError(nil, 503, ""))
}

func TestExtractStreamErrorStatus(t *testing.T) {
	cases := []struct {
		name   string
		errObj any
		msg    string
		want   int
	}{
		{"numeric code field", map[string]any{"code": float64(503)}, "unavailable", 503},
		{"numeric string code field", map[string]any{"code": "503"}, "unavailable", 503},
		{"numeric status field", map[string]any{"status": float64(500)}, "boom", 500},
		{"code wins over status", map[string]any{"code": float64(503), "status": "UNAVAILABLE"}, "boom", 503},
		{"non-numeric code ignored", map[string]any{"code": "context_length_exceeded"}, "too long", 0},
		{"grpc canonical code out of range", map[string]any{"code": float64(3)}, "invalid argument", 0},
		{"bracketed code in message", map[string]any{}, "Streaming response failed: [503] The request queue is full.", 503},
		{"bracketed 400", map[string]any{}, "request returned [400] bad template", 400},
		{"bare code in message", map[string]any{}, "upstream returned 503 for this model", 503},
		{"bare 500 in message", map[string]any{}, "internal error 500", 500},
		{"token count not a status", map[string]any{}, "maximum context length is 4096 tokens", 0},
		{"decimal not a status", map[string]any{}, "sampling temperature 0.400 out of range", 0},
		{"five digit number not a status", map[string]any{}, "limit 40000 tokens exceeded", 0},
		{"no status anywhere", map[string]any{}, "provider error", 0},
		{"non-map error object", "rate limited", "provider error", 0},
		{"nil error object falls to message", nil, "failed with [502] bad gateway", 502},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, ExtractStreamErrorStatus(tc.errObj, tc.msg))
		})
	}
}
