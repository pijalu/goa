// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func (a *Agent) handleStreamFailure(ctx context.Context, streamErr error, model provider.Model, opts provider.StreamOptions) (handled bool, retErr error) {
	a.cfg.Logger.Log(Warn, "stream failure: %v", streamErr)
	// Reset per-round buffers so a retry starts with a clean state. Then undo
	// any assistant message that was appended in the failing round (if any).
	// Hold mu for both operations since they share state.
	a.mu.Lock()
	a.resetStreamRoundState()
	a.mu.Unlock()
	a.undoLastAssistantMessage()

	// Overflow guard: only one compress+retry per turn.  If compression
	// cannot free enough space, the second overflow kills the turn with
	// a clear error instead of retrying into an infinite loop.
	if isContextLengthError(streamErr) {
		if a.overflowRecoveryAttempted {
			a.cfg.Logger.Log(Error, "Overflow recovery failed after compress+retry — giving up")
			a.emitEvent(OutputEvent{Type: EventProgress, Text: "Context overflow recovery failed — compress+retry cycle exhausted. The conversation is too long for this model's context window."})
			return true, fmt.Errorf("context overflow: compression freed insufficient space after retry; try a larger context window model or reset the session")
		}
		a.overflowRecoveryAttempted = true
		a.cfg.Logger.Log(Info, "Overflow recovery: compressing context and retrying once")
	}

	// Classify before retrying. Non-retryable errors (HTTP 400/401/403,
	// malformed request, auth failure) cannot succeed on a second attempt, so
	// surface them immediately with a clear, final message instead of burning
	// the retry budget and delaying the user-visible failure. Overflow is
	// always retryable here (the once-only guard above bounds it).
	// The parent context is passed so that context.Canceled from a transport
	// abort (ctx still alive) is retried, while user-cancel (ctx also done)
	// is surfaced immediately.
	if !shouldRetryStreamError(ctx, streamErr, opts.RetryPolicy) {
		a.cfg.Logger.Log(Warn, "stream error not retryable; surfacing immediately: %v", streamErr)
		a.emitEvent(OutputEvent{
			Type:     EventContent,
			Role:     System,
			Text:     formatFatalStreamMessage(streamErr),
			Metadata: map[string]string{"category": "system-notification"},
		})
		a.emitEvent(OutputEvent{Type: EventProgress, Text: ""})
		return true, fmt.Errorf("LLM request failed (not retryable): %w", streamErr)
	}

	a.cfg.Logger.Log(Warn, "stream error, retrying: %v", streamErr)

	// Surface the failure as a system chat bubble so the user can see the
	// retry in the conversation history, not just a transient status message.
	// The message is NOT marked transient so the error history survives
	// successful retries — the user should know intermittent issues occurred.
	// stream_retry tells the UI to retract the orphaned in-progress assistant
	// bubble: this retry resets contentBuf and re-streams the answer from the
	// start, so without a retraction the partial pre-retry bubble and the
	// re-streamed bubble would both remain, duplicating the text on screen
	// (Issue 4 — streaming repeats that shift on scroll).
	a.emitEvent(OutputEvent{
		Type:     EventContent,
		Role:     System,
		Text:     formatRetryMessage(streamErr),
		Metadata: map[string]string{"category": "system-notification", "stream_retry": "true"},
	})

	toolCallEncountered, retried := a.retryStream(ctx, streamErr, model, opts)
	if retried {
		if !toolCallEncountered {
			return true, nil
		}
		return false, nil
	}

	a.emitEvent(OutputEvent{Type: EventProgress, Text: ""})
	// Surface the final failure after retries are exhausted.
	a.emitEvent(OutputEvent{
		Type:     EventContent,
		Role:     System,
		Text:     formatFatalStreamMessage(streamErr),
		Metadata: map[string]string{"category": "system-notification"},
	})
	if ctx.Err() != nil {
		return true, ctx.Err()
	}
	return true, fmt.Errorf("LLM connection lost after retries: %w", streamErr)
}

// retryStream attempts to reconnect after a stream error. In normal mode it
// retries up to the policy's finite budget; in always mode it retries every
// model-request failure until success or cancellation. Returns whether any
// retry succeeded and whether a tool call was encountered. On context
// cancellation the function returns promptly instead of sleeping through the
// full backoff window.
func (a *Agent) retryStream(ctx context.Context, originalErr error, model provider.Model, opts provider.StreamOptions) (toolCallEncountered bool, retried bool) {
	plan := resolveRetryPlan(opts)
	for retry := 0; ; retry++ {
		if !plan.always && retry >= plan.maxRetries {
			break
		}
		// Wait for the scheduled delay with context awareness so Ctrl+C isn't
		// ignored during backoff. Emits the "scheduled" agent-log event before
		// the wait and the "started" event after it (dsh llm/retry analog).
		if !a.scheduleRetryAttempt(ctx, originalErr, model, plan, retry) {
			return false, false
		}
		stop, tc, retried := a.runRetryAttempt(ctx, model, opts, plan, retry)
		if stop {
			return tc, retried
		}
	}
	return false, false
}

// retryPlan is the resolved retry budget for one stream-failure episode:
// the effective policy, the finite normal-mode budget, and whether always
// mode is active.
type retryPlan struct {
	policy     *provider.RetryPolicy
	maxRetries int
	always     bool
}

// resolveRetryPlan derives the retry plan from the stream options: the
// resolved policy (or the legacy scalar-derived default) and the effective
// finite budget (policy > scalar > 5).
func resolveRetryPlan(opts provider.StreamOptions) retryPlan {
	policy := opts.RetryPolicy
	if policy == nil {
		policy = defaultRetryPolicy(opts)
	}
	maxRetries := policy.MaxRetries
	if maxRetries <= 0 && policy.Mode != provider.RetryModeAlways {
		maxRetries = opts.MaxRetries
	}
	return retryPlan{
		policy:     policy,
		maxRetries: maxRetries,
		always:     policy.Mode == provider.RetryModeAlways,
	}
}

// runRetryAttempt executes one retry attempt and reports whether retryStream
// should stop (returning tc/retried) or continue the loop. Failed attempts
// are cleaned up here so the next attempt starts fresh.
func (a *Agent) runRetryAttempt(ctx context.Context, model provider.Model, opts provider.StreamOptions, plan retryPlan, retry int) (stop bool, tc bool, retried bool) {
	outcome, tc, attemptErr := a.executeRetryAttempt(ctx, model, opts, retry, plan.maxRetries, plan.always)
	switch outcome {
	case retryAttemptSuccess:
		return true, tc, true
	case retryAttemptOpenError:
		// A context-length error on a retry attempt means the overflow
		// recovery (bounded once per turn in handleStreamFailure) freed
		// insufficient space: repeating the same request cannot succeed.
		// Without this, always mode would retry an overflowing request
		// forever.
		if isContextLengthError(attemptErr) {
			return true, false, false
		}
		return false, false, false
	case retryAttemptStreamError:
		// Clean up after the failed retry so the next attempt (or error path)
		// does not inherit partial tokens, buffered tool calls, or a spurious
		// assistant message.
		a.mu.Lock()
		a.resetStreamRoundState()
		a.mu.Unlock()
		a.undoLastAssistantMessage()
		a.cfg.Logger.Log(Warn, "retry attempt %d also failed: %v", retry+1, attemptErr)
		// A context-length error on a retry attempt stops the loop regardless
		// of mode (overflow recovery is bounded once per turn; see the
		// open-error case above).
		if isContextLengthError(attemptErr) {
			return true, false, false
		}
		// A retry attempt that fails with a non-eligible (or canceled) error
		// stops the loop: the next failure is surfaced by handleStreamFailure.
		if !plan.always && !shouldRetryStreamError(ctx, attemptErr, plan.policy) {
			return true, false, false
		}
		return false, false, false
	}
	return false, false, false
}

// scheduleRetryAttempt emits the durable "retry scheduled" agent-log event
// with the computed delay, surfaces the progress bubble, then waits for the
// backoff delay (aborting on cancellation). After the wait it emits the
// "retry started" event. Returns false when the wait was canceled.
func (a *Agent) scheduleRetryAttempt(ctx context.Context, originalErr error, model provider.Model, plan retryPlan, retry int) bool {
	delay := retryBackoff(originalErr, retry, plan.policy)
	a.emitRetryScheduledLog(originalErr, model, plan.policy, retry, plan.maxRetries, plan.always, delay)
	a.emitEvent(OutputEvent{Type: EventProgress, Text: fmt.Sprintf("Reconnecting (attempt %d%s)...", retry+1, retryTotalSuffix(plan.always, plan.maxRetries))})
	select {
	case <-time.After(delay):
	case <-ctx.Done():
		return false
	}
	a.cfg.Logger.Log(Info, "retry started: provider=%s mode=%s attempt=%d%s",
		model.Provider, plan.policy.Mode, retry+1, retryTotalSuffix(plan.always, plan.maxRetries))
	return true
}

// retryAttemptOutcome classifies the result of one retry attempt.
type retryAttemptOutcome int

const (
	// retryAttemptSuccess means the attempt streamed to completion.
	retryAttemptSuccess retryAttemptOutcome = iota
	// retryAttemptOpenError means provider.Stream failed before any events
	// (no partial state to clean up).
	retryAttemptOpenError
	// retryAttemptStreamError means consumeStream failed mid-stream (partial
	// state may need cleanup).
	retryAttemptStreamError
)

// executeRetryAttempt runs one retry attempt: opens the stream and consumes
// it.
func (a *Agent) executeRetryAttempt(ctx context.Context, model provider.Model, opts provider.StreamOptions, retry, maxRetries int, always bool) (retryAttemptOutcome, bool, error) {
	pCtx := a.buildProviderContext(ctx)
	stream, err := a.stream(model, pCtx, opts)
	if err != nil {
		a.cfg.Logger.Log(Warn, "retry stream failed: %v", err)
		return retryAttemptOpenError, false, err
	}
	toolCallEncountered, streamErr := a.consumeStream(ctx, stream, opts)
	if streamErr != nil {
		return retryAttemptStreamError, toolCallEncountered, streamErr
	}
	a.emitEvent(OutputEvent{Type: EventProgress, Text: ""})
	// Durable confirmation so the retry lifecycle is visible in chat
	// history — failure bubble (episode start) + spinner attempts
	// (live) + this restored bubble (success) — not only a transient
	// spinner line (Issue 17).
	a.emitEvent(OutputEvent{
		Type:     EventContent,
		Role:     System,
		Text:     fmt.Sprintf("Connection restored (attempt %d%s) — resuming.", retry+1, retryTotalSuffix(always, maxRetries)),
		Metadata: map[string]string{"category": "system-notification"},
	})
	return retryAttemptSuccess, toolCallEncountered, nil
}

// retryTotalSuffix renders the "/max" budget suffix for progress bubbles.
// Always mode has no finite budget, so it renders the empty suffix (the
// display shows "attempt N" without a total).
func retryTotalSuffix(always bool, maxRetries int) string {
	if always {
		return ""
	}
	return fmt.Sprintf("/%d", maxRetries)
}

// emitRetryScheduledLog writes the durable "retry scheduled" agent-log event
// (dsh llm/retry analog) before the backoff wait.
func (a *Agent) emitRetryScheduledLog(originalErr error, model provider.Model, policy *provider.RetryPolicy, retry, maxRetries int, always bool, delay time.Duration) {
	code := retryCodeOf(originalErr)
	if code == "" {
		code = "UNKNOWN"
	}
	if always {
		a.cfg.Logger.Log(Info, "retry scheduled: provider=%s mode=%s attempt=%d (unbounded) delay=%v code=%s err=%v",
			model.Provider, policy.Mode, retry+1, delay, code, originalErr)
		return
	}
	a.cfg.Logger.Log(Info, "retry scheduled: provider=%s mode=%s attempt=%d/%d delay=%v code=%s err=%v",
		model.Provider, policy.Mode, retry+1, maxRetries, delay, code, originalErr)
}

// defaultRetryPolicy derives a normal-mode policy from the legacy scalar
// StreamOptions fields when no policy was resolved at provider construction.
// It mirrors the historical budget (MaxRetries scalar, MaxRetryDelay cap).
func defaultRetryPolicy(opts provider.StreamOptions) *provider.RetryPolicy {
	maxRetries := opts.MaxRetries
	if maxRetries <= 0 {
		maxRetries = 5
	}
	maxDelay := opts.MaxRetryDelay
	if maxDelay <= 0 {
		maxDelay = 5 * time.Minute
	}
	return &provider.RetryPolicy{
		Mode:       provider.RetryModeNormal,
		MaxRetries: maxRetries,
		Backoff:    provider.RetryBackoff{InitialDelay: time.Second, MaxDelay: maxDelay, Jitter: 0},
		Codes:      nil,
	}
}

func (a *Agent) cacheKey(model provider.Model) string {
	a.mu.Lock()
	defer a.mu.Unlock()
	schemas, _ := json.Marshal(migrateSchemas(a.reg.Schemas(), model))
	sum := sha256.Sum256(schemas)
	return provider.NewCacheKey(provider.CacheIdentity{
		ContextID: a.cacheContextID, Generation: a.cacheGeneration,
		Provider: string(model.Provider), Model: model.ID,
		ToolSchemaHash: hex.EncodeToString(sum[:]),
	})
}

// stream opens a provider stream with the agent's CURRENT conversation cache
// identity stamped on the options, and records that identity so cache-miss
// notices drained at stream completion are attributed to this agent. Every
// provider request derived from the conversation goes through here — initial
// round, re-streams after tool calls, retries, recovery, and the summarize
// call (whose request is the conversation prefix plus an appended
// instruction, i.e. legitimate append semantics). Deriving the key from live
// agent state at open time means a mid-turn generation rotation (compression)
// can never leave a stale identity on a later request (Hard Rule 7).
