// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider/hooks"
)

// maxStreamBackoff caps the delay between stream retries so a huge
// server-supplied Retry-After cannot stall the agent for minutes.
const maxStreamBackoff = 30 * time.Second

// shouldRetryStreamError reports whether err is worth retrying.
//
// It trusts the provider hook classification when the error is a
// *hooks.ProviderError, and otherwise falls back to a transient-error
// heuristic for bare mid-stream failures (idle timeout, dropped connection,
// unexpected EOF). Context cancellation and user-imposed deadlines are never
// retried — retrying them cannot succeed.
//
// Context-overflow errors are always considered retryable here; the
// once-only semantics are enforced separately in handleStreamFailure via
// overflowRecoveryAttempted, so we never loop on compression.
func shouldRetryStreamError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	// Overflow is retried once via the dedicated compress+retry path.
	if isContextLengthError(err) {
		return true
	}
	var provErr *hooks.ProviderError
	if errors.As(err, &provErr) {
		// IsRetryable already incorporates rate-limit, context overflow,
		// configured retryable statuses (5xx/408), 404 (Codex/OpenAI), and
		// transient network errors. Non-retryable 4xx (400/401/403/404-not-
		// codex) return false and are surfaced immediately by the caller.
		return provErr.IsRetryable
	}
	return isTransientStreamError(err)
}

// isTransientStreamError recognizes bare mid-stream failures that the provider
// layer does not wrap in a ProviderError: the synthesized idle-timeout and
// disconnect messages, EOF, and connection resets. These are worth one bounded
// retry.
func isTransientStreamError(err error) bool {
	if err == nil {
		return false
	}
	text := strings.ToLower(err.Error())
	for _, p := range transientStreamPatterns {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

// transientStreamPatterns are lowercased substrings that mark a bare error as
// worth retrying. They intentionally overlap with isRetryableNetworkError in
// the hooks package so bare (unwrapped) variants of the same failures are
// handled consistently.
var transientStreamPatterns = []string{
	"idle timeout",
	"stream disconnected",
	"stream idle",
	"stream ended prematurely", // SSE parser: missing finish_reason/[DONE]
	"ended prematurely",
	"unexpected eof",
	"eof",
	"reset by peer",
	"broken pipe",
	"connection reset",
	"connection refused",
	"no such host",
	"temporarily unavailable",
	"timeout",
}

// retryBackoff computes the delay before the next retry for err.
//
// For rate-limited provider errors it honors a server-supplied Retry-After
// (preferring the millisecond header when present), capped at maxStreamBackoff.
// For everything else it uses bounded exponential backoff (1s, 2s, 4s, ...)
// with up to 250ms of jitter to avoid thundering-herd retries against a
// shared endpoint.
func retryBackoff(err error, attempt int) time.Duration {
	var provErr *hooks.ProviderError
	if errors.As(err, &provErr) && provErr.IsRateLimit {
		if d := retryAfterDuration(provErr.RetryAfter, provErr.RetryAfterMs); d > 0 {
			return clampBackoff(d)
		}
	}
	// Exponential base: attempt 0 -> 1s, 1 -> 2s, 2 -> 4s ... (matches the
	// previous fixed (retry+1) schedule for the first two attempts).
	base := time.Duration(1<<uint(attempt)) * time.Second
	jitter := time.Duration(rand.Intn(250)) * time.Millisecond
	return clampBackoff(base + jitter)
}

// retryAfterDuration converts a Retry-After header value (seconds) and/or a
// Retry-After-Ms header value (milliseconds) into a Duration. The millisecond
// header wins when both are present (higher precision). Zero is returned when
// neither is set.
func retryAfterDuration(seconds, milliseconds int) time.Duration {
	if milliseconds > 0 {
		return time.Duration(milliseconds) * time.Millisecond
	}
	if seconds > 0 {
		return time.Duration(seconds) * time.Second
	}
	return 0
}

// clampBackoff bounds a retry delay to [0, maxStreamBackoff].
func clampBackoff(d time.Duration) time.Duration {
	if d < 0 {
		return 0
	}
	if d > maxStreamBackoff {
		return maxStreamBackoff
	}
	return d
}

// formatFatalStreamMessage renders a non-retryable stream error as a concise
// user-facing message. It is the non-retry counterpart of formatRetryMessage:
// same HTTP-status/body decoding, but no "- retrying" suffix (the error is
// terminal).
func formatFatalStreamMessage(err error) string {
	return formatStreamMessage(err, false)
}

// formatRetryMessage turns a stream error into a concise user-facing message
// that includes the HTTP status, provider message, and error code when
// available, suffixed with "- retrying".
func formatRetryMessage(err error) string {
	return formatStreamMessage(err, true)
}

// formatStreamMessage is the shared renderer for user-facing stream error
// bubbles. When retrying is true, the message is suffixed with "- retrying".
func formatStreamMessage(err error, retrying bool) string {
	suffix := ""
	if retrying {
		suffix = " - retrying"
	}
	var respErr interface {
		StatusCode() int
		ResponseBody() string
	}
	if errors.As(err, &respErr) {
		status := respErr.StatusCode()
		body := respErr.ResponseBody()
		var parsed struct {
			Error struct {
				Message string `json:"message"`
				Code    string `json:"code"`
				Type    string `json:"type"`
			} `json:"error"`
		}
		msg := ""
		code := ""
		if json.Unmarshal([]byte(body), &parsed) == nil && parsed.Error.Message != "" {
			msg = parsed.Error.Message
			code = parsed.Error.Code
		}
		if msg == "" {
			msg = body
		}
		if code != "" {
			return fmt.Sprintf("Error: %d - %s (%s)%s", status, msg, code, suffix)
		}
		return fmt.Sprintf("Error: %d - %s%s", status, msg, suffix)
	}
	return fmt.Sprintf("Error: %s%s", err.Error(), suffix)
}
