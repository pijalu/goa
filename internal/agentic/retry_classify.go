// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/hooks"
)

// maxStreamBackoff caps the delay between stream retries so a huge
// server-supplied Retry-After cannot stall the agent for minutes.
const maxStreamBackoff = 5 * time.Minute

// errEmptyResponse is synthesized when a stream ends cleanly (2xx + [DONE]/EOF)
// but produced no content, no thinking, and no tool calls. Under provider load
// this signals a truncated/failed response, not a legitimate answer, so it is
// retried like any other transient stream failure instead of ending the turn
// silently.
var errEmptyResponse = errors.New("provider returned an empty response (no content, no thinking, no tool calls)")

// retryCodeOf classifies err into the canonical retry failure code
// (EMPTY_RESPONSE, RATE_LIMIT, SERVER, TIMEOUT, TRANSPORT) used by retry
// policy codes[] lists. It returns "" when the error carries no recognized
// provider-neutral code.
func retryCodeOf(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, errEmptyResponse) {
		return provider.RetryCodeEmptyResponse
	}
	var provErr *hooks.ProviderError
	if errors.As(err, &provErr) {
		if provErr.IsRateLimit {
			return provider.RetryCodeRateLimit
		}
		switch code := provErr.StatusCode(); {
		case code >= 500 && code <= 599:
			return provider.RetryCodeServer
		case code == http.StatusRequestTimeout:
			return provider.RetryCodeTimeout
		}
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return provider.RetryCodeTimeout
	}
	text := strings.ToLower(err.Error())
	if isTimeoutText(text) {
		return provider.RetryCodeTimeout
	}
	if isServerErrorText(text) {
		return provider.RetryCodeServer
	}
	if isTransientStreamError(err) {
		return provider.RetryCodeTransport
	}
	return ""
}

// isTimeoutText recognizes timeout-shaped failure text.
func isTimeoutText(text string) bool {
	return strings.Contains(text, "timeout") ||
		strings.Contains(text, "timed out") ||
		strings.Contains(text, "gateway timeout")
}

// isServerErrorText recognizes 5xx-shaped failure text (server-side
// overload/unavailability). "timeout" is excluded so gateway timeouts classify
// as TIMEOUT, not SERVER.
func isServerErrorText(text string) bool {
	for _, p := range []string{
		"queue is full",
		"overloaded",
		"service unavailable",
		"bad gateway",
		"internal server error",
		"upstream connect error",
	} {
		if strings.Contains(text, p) {
			return true
		}
	}
	return false
}

// shouldRetryStreamError reports whether err is worth retrying.
//
// It trusts the provider hook classification when the error is a
// *hooks.ProviderError, and otherwise falls back to a transient-error
// heuristic for bare mid-stream failures (idle timeout, dropped connection,
// unexpected EOF). Deadlines are retried only when the parent context is
// still alive: a request-scoped timeout (transport header timeout) gets a
// fresh request on retry, while a parent-imposed deadline cannot succeed
// and is surfaced immediately.
//
// Context cancellation requires special handling: when the outer context
// (parentCtx) is still alive but the stream error is context.Canceled,
// the transport dropped the connection server-side (e.g. a proxy or load
// balancer killed the HTTP connection). This is retryable. When the outer
// context is also canceled (user pressed Escape/Ctrl+C), it is a genuine
// user cancel and must NOT be retried.
//
// Context-overflow errors are always considered retryable here; the
// once-only semantics are enforced separately in handleStreamFailure via
// overflowRecoveryAttempted, so we never loop on compression.
//
// An optional retry policy adjusts the decision:
//   - always mode retries every failure until the parent context is done
//     (user cancel), regardless of code eligibility.
//   - normal mode with a non-empty Codes list retries only failures whose
//     canonical code is in the list.
//   - nil policy keeps the legacy classification exactly.
func shouldRetryStreamError(parentCtx context.Context, err error, policies ...*provider.RetryPolicy) bool {
	if err == nil {
		return false
	}
	// Policy short-circuits: a dead parent context (user/turn cancel) is never
	// retried; always mode retries every failure until cancellation; a
	// normal-mode codes filter gates eligibility. A nil policy lets the legacy
	// classification below decide.
	if decision, decided := policyRetryDecision(parentCtx, err, retryPolicyOf(policies...)); decided {
		return decision
	}
	// Deadline exceeded: same discrimination as context.Canceled below.
	// When the outer context is still alive, the deadline that fired was
	// request-scoped — e.g. the transport's ResponseHeaderTimeout, which Go
	// unwraps to context.DeadlineExceeded — and a retry issues a fresh
	// request that can succeed (LM Studio still loading the model). When
	// the outer context's own deadline fired (user-imposed turn deadline),
	// retrying cannot succeed and the error is surfaced immediately.
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	// Context cancellation: distinguish transport abort from user cancel.
	// When the outer context is still alive (parentCtx.Err() == nil), a
	// context.Canceled stream error means the transport dropped the
	// connection server-side — retryable. When the outer context is also
	// canceled, the user pressed Escape/Ctrl+C — not retryable.
	if errors.Is(err, context.Canceled) {
		return true
	}
	// An empty clean response is a provider-side truncation (seen under load);
	// worth a bounded retry rather than a silent turn end.
	if errors.Is(err, errEmptyResponse) {
		return true
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

// retryPolicyOf extracts the first optional policy, or nil when none was
// passed. Keeps the variadic call sites backward compatible.
func retryPolicyOf(policies ...*provider.RetryPolicy) *provider.RetryPolicy {
	if len(policies) > 0 {
		return policies[0]
	}
	return nil
}

// policyRetryDecision applies the policy-level short-circuits shared by every
// retry decision. decided=false means no policy constraint applies and the
// legacy classification should run. decided=true carries the forced outcome:
// false for a dead parent context (user/turn cancel) or a codes-rejected
// failure, true for always mode.
func policyRetryDecision(parentCtx context.Context, err error, policy *provider.RetryPolicy) (decision, decided bool) {
	if parentCtx != nil && parentCtx.Err() != nil {
		return false, true
	}
	if policy == nil {
		return false, false
	}
	if policy.Mode == provider.RetryModeAlways {
		return true, true
	}
	if !policyAllowsCode(err, policy.Codes) {
		return false, true
	}
	return false, false
}

// policyAllowsCode reports whether err's canonical retry code is eligible
// under the policy's codes list. An empty list means no restriction (the
// default transient set applies). Context-overflow errors always pass: they
// are handled by the dedicated once-only compress+retry path with its own
// budget, never by the policy code list (dsh semantics: context-overflow
// compaction owns a separate budget).
func policyAllowsCode(err error, codes []string) bool {
	if len(codes) == 0 {
		return true
	}
	if isContextLengthError(err) {
		return true
	}
	code := retryCodeOf(err)
	return code != "" && stringInSlice(code, codes)
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
// handled consistently. The 5xx-text entries are the last-resort net for
// mid-stream provider error frames that carry no parseable status code
// (parse-layer classification in hooks.NewStreamFrameError is the primary
// path): a queue-full/overloaded server is transient by definition.
var transientStreamPatterns = []string{
	"idle timeout",
	"stream disconnected",
	"stream idle",
	"stream stalled", // event-level stall watchdog (consumeStream): provider sent keep-alives but no real events
	"no events received",
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
	// 5xx-as-text: mid-stream error frames without a status code.
	"queue is full", // llama.cpp/LM Studio "The request queue is full."
	"overloaded",    // Anthropic overloaded_error, gateway overloads
	"service unavailable",
	"bad gateway",
	"gateway timeout",
	"internal server error",
	"upstream connect error", // Envoy proxy upstream failure
}

// retryBackoff computes the delay before the next retry for err.
//
// For rate-limited provider errors it honors a server-supplied Retry-After
// (preferring the millisecond header when present). With a policy the delay is
// capped at the policy's backoff.MaxDelay and an over-cap provider delay falls
// back to local backoff (dsh semantics); without a policy the historical
// clampBackoff (maxStreamBackoff) applies.
//
// For everything else it uses bounded exponential backoff with the policy's
// initial/max/jitter when provided, or the legacy schedule (1s, 2s, 4s, ...)
// with up to 250ms of jitter to avoid thundering-herd retries against a
// shared endpoint.
func retryBackoff(err error, attempt int, policies ...*provider.RetryPolicy) time.Duration {
	policy := retryPolicyOf(policies...)
	if d, handled := retryAfterBackoff(err, policy); handled {
		return d
	}
	if isRateLimitError(err) {
		return rateLimitBackoff(attempt, policy)
	}
	if policy != nil {
		return policyBackoff(policy, attempt)
	}
	// Legacy schedule: attempt 0 -> 1s, 1 -> 2s, 2 -> 4s ... (matches the
	// previous fixed (retry+1) schedule for the first two attempts).
	base := time.Duration(1<<uint(attempt)) * time.Second
	jitter := time.Duration(rand.Intn(250)) * time.Millisecond
	return clampBackoff(base + jitter)
}

func isRateLimitError(err error) bool {
	var provErr *hooks.ProviderError
	return errors.As(err, &provErr) && provErr.IsRateLimit
}

func retryAfterBackoff(err error, policy *provider.RetryPolicy) (time.Duration, bool) {
	var provErr *hooks.ProviderError
	if !errors.As(err, &provErr) || !provErr.IsRateLimit {
		return 0, false
	}
	d := retryAfterDuration(provErr.RetryAfter, provErr.RetryAfterMs)
	if d <= 0 {
		return 0, false
	}
	if policy != nil && policy.Backoff.MaxDelay > 0 {
		if d <= policy.Backoff.MaxDelay {
			return d, true
		}
		return 0, false
	}
	return clampBackoff(d), true
}

// rateLimitBackoff computes Fibonacci growth for rate-limit failures. The
// first two retries both wait the initial delay; later waits sum the prior two.
func rateLimitBackoff(attempt int, policy *provider.RetryPolicy) time.Duration {
	initial, maxDelay, jitter := time.Second, maxStreamBackoff, float64(0)
	if policy != nil {
		if policy.Backoff.InitialDelay > 0 {
			initial = policy.Backoff.InitialDelay
		}
		if policy.Backoff.MaxDelay > 0 {
			maxDelay = policy.Backoff.MaxDelay
		}
		jitter = policy.Backoff.Jitter
	}
	if attempt < 0 {
		attempt = 0
	}
	a, b := initial, initial
	for i := 1; i < attempt; i++ {
		if b >= maxDelay-a {
			b = maxDelay
			break
		}
		a, b = b, a+b
	}
	if b > maxDelay {
		b = maxDelay
	}
	if jitter <= 0 {
		return b
	}
	if jitter > 1 {
		jitter = 1
	}
	d := time.Duration(float64(b) * (1 - jitter + 2*jitter*rand.Float64()))
	if d > maxDelay {
		return maxDelay
	}
	return d
}

// policyBackoff computes the local exponential-backoff delay for one retry
// under a resolved retry policy: initial * 2^attempt, capped at max, with
// symmetric ratio jitter around one (dsh semantics). attempt is 0-based.
func policyBackoff(policy *provider.RetryPolicy, attempt int) time.Duration {
	initial := policy.Backoff.InitialDelay
	if initial <= 0 {
		initial = time.Second
	}
	maxDelay := policy.Backoff.MaxDelay
	if maxDelay <= 0 {
		maxDelay = maxStreamBackoff
	}
	jitter := policy.Backoff.Jitter
	if jitter < 0 {
		jitter = 0
	}
	if jitter > 1 {
		jitter = 1
	}
	base := initial << uint(attempt)
	if base > maxDelay {
		base = maxDelay
	}
	if jitter == 0 {
		return base
	}
	factor := 1 - jitter + 2*jitter*rand.Float64()
	d := time.Duration(float64(base) * factor)
	if d > maxDelay {
		d = maxDelay
	}
	if d < 0 {
		return 0
	}
	return d
}

// stringInSlice reports whether s is present in list.
func stringInSlice(s string, list []string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
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
		if status == 0 && body == "" {
			// Non-HTTP failure (connection timeout/refused/reset): no status
			// line or body exists to decode — "Error: 0 - " carries zero
			// information. Render the underlying error text instead.
			return fmt.Sprintf("Error: %s%s", err.Error(), suffix)
		}
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
		msg += actionableHint(msg)
		if code != "" {
			return fmt.Sprintf("Error: %d - %s (%s)%s", status, msg, code, suffix)
		}
		return fmt.Sprintf("Error: %d - %s%s", status, msg, suffix)
	}
	return fmt.Sprintf("Error: %s%s", err.Error(), suffix)
}

// actionableHint appends a short, actionable fix hint for provider errors that
// name a request parameter the user can change. Today it covers the
// fixed-temperature rejection (e.g. kimi-code "invalid temperature: only 1 is
// allowed for this model"): point the user at the exact setting to change
// instead of leaving them with a bare 400.
func actionableHint(msg string) string {
	lower := strings.ToLower(msg)
	if strings.Contains(lower, "invalid temperature") ||
		(strings.Contains(lower, "temperature") && strings.Contains(lower, "allowed")) {
		return " — this endpoint only accepts its default temperature; remove the model's temperature setting (/config → Models) or set it to the allowed value"
	}
	return ""
}
