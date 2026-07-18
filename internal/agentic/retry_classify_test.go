// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/hooks"
	"github.com/stretchr/testify/assert"
)

func TestShouldRetryStreamError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"context canceled", context.Canceled, false},
		{"context deadline", context.DeadlineExceeded, false},

		// Context overflow is always retryable here (once-only guard lives in
		// the caller).
		{"overflow bare", errors.New("context_length_exceeded"), true},
		{"overflow provider", &hooks.ProviderError{IsContextOverflow: true, IsRetryable: true}, true},

		// Provider classification is trusted.
		{"retryable 5xx", &hooks.ProviderError{IsRetryable: true}, true},
		{"rate limit", &hooks.ProviderError{IsRateLimit: true, IsRetryable: true}, true},
		{"non-retryable 400", &hooks.ProviderError{IsRetryable: false}, false},
		{"non-retryable 401", &hooks.ProviderError{IsRetryable: false}, false},

		// Bare transient errors are recognized even without a ProviderError.
		{"idle timeout bare", errors.New("stream idle timeout: no data"), true},
		{"premature SSE bare", errors.New("SSE stream ended prematurely: no finish_reason"), true},
		{"connection reset bare", errors.New("read tcp: connection reset by peer"), true},
		{"unrecognized bare", errors.New("something else entirely"), false},

		// The event-level stall watchdog error (consumeStream CloseWithError)
		// is a bare fmt.Errorf, not a ProviderError — it must be retryable so a
		// provider that sends keep-alives but no real events gets a bounded
		// retry instead of killing the turn as "not retryable".
		{"event stall watchdog", errors.New("stream stalled: no events received from provider for 2m0s"), true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, shouldRetryStreamError(tc.err))
		})
	}
}

func TestRetryBackoffHonorsRetryAfter(t *testing.T) {
	// Rate limit with Retry-After in seconds.
	rl := &hooks.ProviderError{IsRateLimit: true, IsRetryable: true, RetryAfter: 12}
	assert.Equal(t, 12*time.Second, retryBackoff(rl, 0))

	// Millisecond header wins over seconds when present.
	rlMs := &hooks.ProviderError{IsRateLimit: true, IsRetryable: true, RetryAfter: 12, RetryAfterMs: 2500}
	assert.Equal(t, 2500*time.Millisecond, retryBackoff(rlMs, 0))

	// Rate limit with no header falls back to exponential backoff.
	rlNoHeader := &hooks.ProviderError{IsRateLimit: true, IsRetryable: true}
	d := retryBackoff(rlNoHeader, 0)
	assert.GreaterOrEqual(t, d, time.Second)
	assert.LessOrEqual(t, d, 1250*time.Millisecond) // 1s base + up to 250ms jitter

	// Non-rate-limit error uses exponential base (attempt 1 -> 2s).
	d1 := retryBackoff(errors.New("boom"), 1)
	assert.GreaterOrEqual(t, d1, 2*time.Second)
	assert.LessOrEqual(t, d1, 2250*time.Millisecond)
}

func TestRetryBackoffCapped(t *testing.T) {
	// A maliciously large Retry-After is capped.
	rl := &hooks.ProviderError{IsRateLimit: true, IsRetryable: true, RetryAfter: 3600}
	assert.Equal(t, maxStreamBackoff, retryBackoff(rl, 0))
}

func TestFormatFatalStreamMessage(t *testing.T) {
	// Non-retryable bubbles must NOT carry the "- retrying" suffix.
	msg := formatFatalStreamMessage(errors.New("bad request body"))
	assert.Contains(t, msg, "Error: bad request body")
	assert.NotContains(t, msg, "retrying")

	// The retry counterpart keeps the suffix (back-compat with existing tests).
	assert.Contains(t, formatRetryMessage(errors.New("boom")), "- retrying")
}

// TestFormatFatalStreamMessage and friends above cover the retry decision
// and backoff helpers introduced alongside shouldRetryStreamError/retryBackoff.
