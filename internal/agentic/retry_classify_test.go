// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/hooks"
	"github.com/stretchr/testify/assert"
)

func TestShouldRetryStreamError(t *testing.T) {
	// All tests use a live (non-canceled) parent context, simulating the
	// transport-abort scenario where the outer context is still active.
	liveCtx := context.Background()

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		// Transport abort: context.Canceled with a live parent context
		// is retryable (server-side connection drop).
		{"context canceled (transport abort)", context.Canceled, true},
		// Request-scoped deadline with a live parent: the transport's
		// ResponseHeaderTimeout unwraps to context.DeadlineExceeded (Go
		// 1.26), so a slow-to-respond LLM server (model still loading)
		// surfaces as DeadlineExceeded even though a fresh request can
		// succeed. Must be retried like any other transient failure.
		{"context deadline (request-scoped)", context.DeadlineExceeded, true},
		{"header timeout (url.Error)", headerTimeoutErr(), true},
		{"header timeout provider error", &hooks.ProviderError{Err: headerTimeoutErr(), IsRetryable: true}, true},

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
			assert.Equal(t, tc.want, shouldRetryStreamError(liveCtx, tc.err))
		})
	}
}

// headerTimeoutErr synthesizes the error shape Go's http.Transport returns
// when ResponseHeaderTimeout fires: a *url.Error whose chain unwraps to
// context.DeadlineExceeded (verified against Go 1.26).
func headerTimeoutErr() error {
	return &url.Error{
		Op:  "Post",
		URL: "http://localhost:1234/v1/chat/completions",
		Err: fmt.Errorf("net/http: timeout awaiting response headers: %w", context.DeadlineExceeded),
	}
}

// TestShouldRetryStreamError_UserCancel verifies that context.Canceled with a
// canceled parent context (user pressed Escape/Ctrl+C) is NOT retried.
func TestShouldRetryStreamError_UserCancel(t *testing.T) {
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel() // immediately cancel
	assert.False(t, shouldRetryStreamError(cancelledCtx, context.Canceled),
		"context.Canceled with canceled parent context (user cancel) must not be retried")
}

// TestShouldRetryStreamError_ParentDeadlineExpired verifies that a
// DeadlineExceeded stream error is NOT retried when the parent (turn)
// context's own deadline has fired: a retry would fail immediately against
// the same dead context, so the error is surfaced instead.
func TestShouldRetryStreamError_ParentDeadlineExpired(t *testing.T) {
	expiredCtx, cancel := context.WithTimeout(context.Background(), time.Nanosecond)
	defer cancel()
	time.Sleep(time.Millisecond) // let the deadline fire
	assert.False(t, shouldRetryStreamError(expiredCtx, context.DeadlineExceeded),
		"DeadlineExceeded with expired parent context (turn deadline) must not be retried")
	assert.False(t, shouldRetryStreamError(expiredCtx, headerTimeoutErr()),
		"header-timeout-shaped error with expired parent context must not be retried")
}

func TestRetryBackoffHonorsRetryAfter(t *testing.T) {
	// Rate limit with Retry-After in seconds.
	rl := &hooks.ProviderError{IsRateLimit: true, IsRetryable: true, RetryAfter: 12}
	assert.Equal(t, 12*time.Second, retryBackoff(rl, 0))

	// Millisecond header wins over seconds when present.
	rlMs := &hooks.ProviderError{IsRateLimit: true, IsRetryable: true, RetryAfter: 12, RetryAfterMs: 2500}
	assert.Equal(t, 2500*time.Millisecond, retryBackoff(rlMs, 0))

	// Rate limit with no header follows Fibonacci backoff without jitter.
	rlNoHeader := &hooks.ProviderError{IsRateLimit: true, IsRetryable: true}
	for attempt, want := range []time.Duration{time.Second, time.Second, 2 * time.Second, 3 * time.Second, 5 * time.Second, 8 * time.Second} {
		assert.Equal(t, want, retryBackoff(rlNoHeader, attempt))
	}

	// Non-rate-limit error uses exponential base (attempt 1 -> 2s).
	d1 := retryBackoff(errors.New("boom"), 1)
	assert.GreaterOrEqual(t, d1, 2*time.Second)
	assert.LessOrEqual(t, d1, 2250*time.Millisecond)
}

func TestRetryBackoffCapped(t *testing.T) {
	// A maliciously large Retry-After is capped.
	rl := &hooks.ProviderError{IsRateLimit: true, IsRetryable: true, RetryAfter: 3600}
	assert.Equal(t, maxStreamBackoff, retryBackoff(rl, 0))
	assert.Equal(t, maxStreamBackoff, retryBackoff(&hooks.ProviderError{IsRateLimit: true, IsRetryable: true}, 30))
}

// TestShouldRetryStreamError_MidStream5xxFrame is the end-to-end regression
// for the reported failure:
//
//	Error: chunk decode failed: LLM error: Streaming response failed:
//	[503] The request queue is full.   → goal paused, zero retries.
//
// The error frame bypasses the transport's error hook (HTTP 200), so the
// parse layer classifies it into a *hooks.ProviderError (all 5xx are
// intrinsically retryable); the agent's retry loop must honor that through
// the "chunk decode failed" wrapping.
func TestShouldRetryStreamError_MidStream5xxFrame(t *testing.T) {
	liveCtx := context.Background()

	frameErr := hooks.NewStreamFrameError(
		fmt.Errorf("LLM error: Streaming response failed: [503] The request queue is full."),
		503,
		`{"error":{"message":"Streaming response failed: [503] The request queue is full."}}`,
	)
	streamErr := fmt.Errorf("chunk decode failed: %w", frameErr)

	assert.True(t, shouldRetryStreamError(liveCtx, streamErr),
		"mid-stream 503 error frame must be retried, not kill the turn")

	// The retry bubble must render the decoded status + provider message.
	msg := formatRetryMessage(streamErr)
	assert.Contains(t, msg, "503")
	assert.Contains(t, msg, "request queue is full")
	assert.Contains(t, msg, "retrying")
}

// TestShouldRetryStreamError_MidStream5xxTextSafetyNet covers error frames
// whose status cannot be parsed at all (no code field, no bracketed/bare
// code): the 5xx-as-text patterns keep them retryable.
func TestShouldRetryStreamError_MidStream5xxTextSafetyNet(t *testing.T) {
	liveCtx := context.Background()
	cases := []struct {
		name string
		err  error
	}{
		{"queue full no code", fmt.Errorf("chunk decode failed: %w", errors.New("LLM error: The request queue is full."))},
		{"overloaded", errors.New("anthropic error: overloaded — try again later")},
		{"service unavailable", errors.New("LLM error: 503 Service Unavailable")},
		{"bad gateway", errors.New("stream failed: 502 Bad Gateway")},
		{"gateway timeout", errors.New("LLM error: 504 Gateway Timeout")},
		{"internal server error", errors.New("LLM error: Internal Server Error")},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			assert.True(t, shouldRetryStreamError(liveCtx, tc.err),
				"bare 5xx-text stream error must be retried: %v", tc.err)
		})
	}
}

func TestFormatFatalStreamMessage(t *testing.T) {
	// Non-retryable bubbles must NOT carry the "- retrying" suffix.
	msg := formatFatalStreamMessage(errors.New("bad request body"))
	assert.Contains(t, msg, "Error: bad request body")
	assert.NotContains(t, msg, "retrying")

	// The retry counterpart keeps the suffix (back-compat with existing tests).
	assert.Contains(t, formatRetryMessage(errors.New("boom")), "- retrying")
}

// Regression (bug: temperature 400): a fixed-temperature rejection
// ("invalid temperature: only 1 is allowed for this model") must render an
// actionable hint telling the user which setting to change, not a bare 400.
func TestFormatFatalStreamMessage_TemperatureHint(t *testing.T) {
	body := `{"error":{"message":"invalid temperature: only 1 is allowed for this model","type":"invalid_request_error"}}`
	provErr := (&hooks.ErrorContext{StatusCode: 400, Body: body}).ToError()

	msg := formatFatalStreamMessage(provErr)
	assert.Contains(t, msg, "invalid temperature", "should keep the provider message")
	assert.Contains(t, msg, "temperature setting", "should append the actionable fix hint")
	assert.Contains(t, msg, "/config", "should point at where to change it")

	// A non-temperature 400 gets no hint.
	other := (&hooks.ErrorContext{StatusCode: 400, Body: `{"error":{"message":"bad request"}}`}).ToError()
	assert.NotContains(t, formatFatalStreamMessage(other), "temperature setting")
}

// TestFormatStreamMessage_NonHTTPProviderError pins the "Error: 0 -" defect:
// a ProviderError with status 0 and an empty body (connection timeout,
// refused, reset — no HTTP response ever arrived) must render the underlying
// error text, not the meaningless "Error: 0 - " status line.
func TestFormatStreamMessage_NonHTTPProviderError(t *testing.T) {
	provErr := &hooks.ProviderError{Err: headerTimeoutErr(), IsRetryable: true}

	fatal := formatFatalStreamMessage(provErr)
	assert.Contains(t, fatal, "timeout awaiting response headers")
	assert.NotContains(t, fatal, "Error: 0 -")

	retrying := formatRetryMessage(provErr)
	assert.Contains(t, retrying, "timeout awaiting response headers")
	assert.Contains(t, retrying, "- retrying")
	assert.NotContains(t, retrying, "Error: 0 -")
}

// TestFormatFatalStreamMessage and friends above cover the retry decision
// and backoff helpers introduced alongside shouldRetryStreamError/retryBackoff.

// --- P8 (DS4): per-provider retry policy ---

func TestRetryCodeOf(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want string
	}{
		{"nil", nil, ""},
		{"empty response", errEmptyResponse, provider.RetryCodeEmptyResponse},
		{"rate limit provider", &hooks.ProviderError{IsRateLimit: true, IsRetryable: true}, provider.RetryCodeRateLimit},
		{"5xx provider", (&hooks.ErrorContext{StatusCode: 503, Body: `{"error":{"message":"boom"}}`, IsRetryable: true}).ToError(), provider.RetryCodeServer},
		{"408 provider", (&hooks.ErrorContext{StatusCode: 408, Body: `{"error":{"message":"timeout"}}`, IsRetryable: true}).ToError(), provider.RetryCodeTimeout},
		{"deadline exceeded", context.DeadlineExceeded, provider.RetryCodeTimeout},
		{"idle timeout bare", errors.New("stream idle timeout: no data"), provider.RetryCodeTimeout},
		{"connection reset bare", errors.New("read tcp: connection reset by peer"), provider.RetryCodeTransport},
		{"5xx text bare", errors.New("LLM error: The request queue is full."), provider.RetryCodeServer},
		{"unrecognized bare", errors.New("something else entirely"), ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, retryCodeOf(tc.err))
		})
	}
}

// TestShouldRetryStreamError_AlwaysMode verifies that an always-mode policy
// retries every model-request failure — including ones the legacy classifier
// marks non-retryable (400/401) — until the parent context is canceled.
func TestShouldRetryStreamError_AlwaysMode(t *testing.T) {
	liveCtx := context.Background()
	always := &provider.RetryPolicy{Mode: provider.RetryModeAlways}

	tests := []struct {
		name string
		err  error
	}{
		{"non-retryable 400", &hooks.ProviderError{IsRetryable: false}},
		{"non-retryable 401", &hooks.ProviderError{IsRetryable: false}},
		{"bare unrecognized", errors.New("whatever")},
		{"rate limit", &hooks.ProviderError{IsRateLimit: true, IsRetryable: true}},
		{"empty response", errEmptyResponse},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.True(t, shouldRetryStreamError(liveCtx, tc.err, always),
				"always mode must retry: %v", tc.err)
		})
	}

	// User cancel still wins: a dead parent context is never retried.
	cancelledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	assert.False(t, shouldRetryStreamError(cancelledCtx, &hooks.ProviderError{IsRetryable: false}, always),
		"always mode must stop on user cancel")
}

// TestShouldRetryStreamError_CodesFilter verifies that a normal-mode policy
// with a codes[] list restricts retries to the listed failure codes.
func TestShouldRetryStreamError_CodesFilter(t *testing.T) {
	liveCtx := context.Background()
	rateLimitOnly := &provider.RetryPolicy{
		Mode:  provider.RetryModeNormal,
		Codes: []string{provider.RetryCodeRateLimit},
	}
	rateLimitErr := &hooks.ProviderError{IsRateLimit: true, IsRetryable: true}
	serverErr := (&hooks.ErrorContext{StatusCode: 503, Body: `{"error":{"message":"boom"}}`, IsRetryable: true}).ToError()

	assert.True(t, shouldRetryStreamError(liveCtx, rateLimitErr, rateLimitOnly),
		"rate limit must be retried when RATE_LIMIT is eligible")
	assert.False(t, shouldRetryStreamError(liveCtx, serverErr, rateLimitOnly),
		"server error must NOT be retried when only RATE_LIMIT is eligible")
	assert.False(t, shouldRetryStreamError(liveCtx, errors.New("unknown failure"), rateLimitOnly),
		"unclassifiable error must not be retried under a codes filter")
}

// TestShouldRetryStreamError_CodesFilter_OverflowBypasses verifies that
// context-overflow errors are always eligible for the once-only compress+retry
// even under a restrictive codes list (dsh: overflow compaction owns a
// separate budget).
func TestShouldRetryStreamError_CodesFilter_OverflowBypasses(t *testing.T) {
	liveCtx := context.Background()
	rateLimitOnly := &provider.RetryPolicy{
		Mode:  provider.RetryModeNormal,
		Codes: []string{provider.RetryCodeRateLimit},
	}
	assert.True(t, shouldRetryStreamError(liveCtx, errors.New("context_length_exceeded"), rateLimitOnly),
		"overflow must bypass the codes filter")
}

// TestShouldRetryStreamError_CodesFilter_EmptyCodesUsesDefault verifies an
// empty codes list does not filter (default transient set applies).
func TestShouldRetryStreamError_CodesFilter_EmptyCodesUsesDefault(t *testing.T) {
	liveCtx := context.Background()
	policy := &provider.RetryPolicy{Mode: provider.RetryModeNormal}
	assert.True(t, shouldRetryStreamError(liveCtx, &hooks.ProviderError{IsRetryable: true}, policy),
		"empty codes must not restrict retries")
}

// TestRetryBackoff_PolicySchedule verifies policy backoff: initial * 2^attempt
// with symmetric ratio jitter, capped at max.
func TestRetryBackoff_PolicySchedule(t *testing.T) {
	policy := &provider.RetryPolicy{
		Mode: provider.RetryModeNormal,
		Backoff: provider.RetryBackoff{
			InitialDelay: 500 * time.Millisecond,
			MaxDelay:     10 * time.Second,
			Jitter:       0.1,
		},
	}
	d0 := retryBackoff(errors.New("boom"), 0, policy)
	assert.GreaterOrEqual(t, d0, 450*time.Millisecond)
	assert.LessOrEqual(t, d0, 550*time.Millisecond)

	d2 := retryBackoff(errors.New("boom"), 2, policy)
	assert.GreaterOrEqual(t, d2, 1800*time.Millisecond)
	assert.LessOrEqual(t, d2, 2200*time.Millisecond)
}

// TestRetryBackoff_PolicyNoJitter verifies zero jitter yields the exact
// exponential base (deterministic).
func TestRetryBackoff_PolicyNoJitter(t *testing.T) {
	policy := &provider.RetryPolicy{
		Mode: provider.RetryModeNormal,
		Backoff: provider.RetryBackoff{
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     time.Second,
			Jitter:       0,
		},
	}
	assert.Equal(t, 100*time.Millisecond, retryBackoff(errors.New("boom"), 0, policy))
	assert.Equal(t, 200*time.Millisecond, retryBackoff(errors.New("boom"), 1, policy))
	assert.Equal(t, 400*time.Millisecond, retryBackoff(errors.New("boom"), 2, policy))
}

// TestRetryBackoff_PolicyOverCapRetryAfter verifies dsh semantics: an
// over-cap provider Retry-After falls back to local backoff (normal mode)
// instead of honoring the huge delay.
func TestRetryBackoff_PolicyOverCapRetryAfter(t *testing.T) {
	policy := &provider.RetryPolicy{
		Mode: provider.RetryModeNormal,
		Backoff: provider.RetryBackoff{
			InitialDelay: 100 * time.Millisecond,
			MaxDelay:     time.Second,
			Jitter:       0,
		},
	}
	rl := &hooks.ProviderError{IsRateLimit: true, IsRetryable: true, RetryAfter: 3600}
	assert.Equal(t, 100*time.Millisecond, retryBackoff(rl, 0, policy),
		"over-cap Retry-After must fall back to local backoff")

	// An in-cap Retry-After is honored exactly (no jitter).
	rlIn := &hooks.ProviderError{IsRateLimit: true, IsRetryable: true, RetryAfterMs: 500}
	assert.Equal(t, 500*time.Millisecond, retryBackoff(rlIn, 0, policy))
}
