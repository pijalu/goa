// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"net/http"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A 408 (Request Timeout) is returned by some LLM servers and proxies when the
// connection/generation stalls. Although 408 is technically a client-error class
// status, it is a transient condition that often succeeds on retry, so the
// default profile must classify it as retryable. Without this, the agent surfaces
// the error immediately instead of retrying with backoff.
func TestErrorHook_408RetryableWithDefaultProfile(t *testing.T) {
	h := &ErrorHook{}
	profile := schema.DefaultProfile(schema.Model{
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderLMStudio,
	})
	require.NoError(t, h.Init(profile))

	ctx := &ErrorContext{
		StatusCode: http.StatusRequestTimeout,
		Body:       `{"error":{"message":"request timeout","type":"request_timeout"}}`,
		Profile:    profile,
	}
	require.NoError(t, h.ApplyError(ctx))
	assert.True(t, ctx.IsRetryable, "HTTP 408 must be retryable with the default profile")
}

// A 400 (Bad Request) is a permanent client error and must remain non-retryable
// even with the default profile, so retries are not wasted on malformed requests.
func TestErrorHook_400NotRetryableWithDefaultProfile(t *testing.T) {
	h := &ErrorHook{}
	profile := schema.DefaultProfile(schema.Model{
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderLMStudio,
	})
	require.NoError(t, h.Init(profile))

	ctx := &ErrorContext{
		StatusCode: http.StatusBadRequest,
		Body:       `{"error":{"message":"bad request","type":"invalid_request"}}`,
		Profile:    profile,
	}
	require.NoError(t, h.ApplyError(ctx))
	assert.False(t, ctx.IsRetryable, "HTTP 400 must not be retryable")
}
