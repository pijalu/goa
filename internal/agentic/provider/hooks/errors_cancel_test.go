// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// A deliberate user cancellation (Escape / Ctrl+C / CloseStreamOnCancel) must
// never be classified retryable — retrying a cancel the user asked for would
// reconnect a stream they explicitly aborted. The documented contract in
// retry_classify.go states bare context.Canceled stays non-retryable, but
// isRetryableNetworkError substring-matches "context canceled", which flips a
// wrapped cancel to retryable. Regression test for that contradiction.
func TestErrorHook_ContextCanceledNotRetryable(t *testing.T) {
	h := &ErrorHook{}
	profile := schema.VariantProfile{
		ID:    "openai-base",
		Match: schema.ProfileMatch{Provider: "openai"},
	}
	require.NoError(t, h.Init(profile))

	cases := []struct {
		name string
		err  error
	}{
		{"bare context.Canceled", context.Canceled},
		{"wrapped context.Canceled", fmt.Errorf("stream read: %w", context.Canceled)},
		{"synthesized 'context canceled' text", errors.New("post \"https://x\": context canceled")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := &ErrorContext{Err: tc.err, Profile: profile}
			require.NoError(t, h.ApplyError(ctx))
			assert.False(t, ctx.IsRetryable,
				"a user-initiated cancel must not be retryable: %v", tc.err)
		})
	}
}

// Genuine transient network errors stay retryable (guard against over-correction).
func TestErrorHook_TransientNetworkStillRetryable(t *testing.T) {
	h := &ErrorHook{}
	require.NoError(t, h.Init(schema.VariantProfile{}))
	for _, msg := range []string{
		"dial tcp: connection refused",
		"unexpected EOF",
		"read: connection reset by peer",
	} {
		ctx := &ErrorContext{Err: errors.New(msg)}
		require.NoError(t, h.ApplyError(ctx))
		assert.True(t, ctx.IsRetryable, "transient network error should stay retryable: %s", msg)
	}
}
