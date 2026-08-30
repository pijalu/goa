// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func (a *Agent) processTurnWithStream(ctx context.Context) error {
	a.cfg.Logger.Log(Debug, "Agent.processTurnWithStream started")
	// Strip transient (ephemeral) system nudges at turn end so recovery/repeat
	// hints inform the model during this turn but do not pollute future turns.
	defer a.stripEphemeralSystemMessages()
	// Safety net: the thinking-stall timers are already disarmed on content/
	// tool progress and at every round boundary; stop them here too so an
	// early error return can never leak a live timer past the turn.
	defer a.stopThinkingStallTimers()

	model, opts, initCtx := a.prepareTurn(ctx)
	if err := a.checkContextLimit(); err != nil {
		return err
	}

	maxRounds := a.effectiveMaxStreamRounds()

	for round := 0; ; round++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		// Explicit per-turn round cap. The loop is otherwise convergence-driven
		// only (Issue 13); MaxStreamRounds > 0 restores a numeric bound for
		// callers that want one. Zero/unset disables the cap — there is
		// deliberately no hidden default (same contract as
		// MaxConsecutiveToolRounds; the application layer supplies defaults).
		if maxRounds > 0 && round >= maxRounds {
			if err := a.runRecoveryStream(ctx, model, opts,
				fmt.Sprintf("per-turn stream round limit (%d) reached", maxRounds)); err != nil {
				return err
			}
			return nil
		}
		done, err := a.runStreamRound(ctx, round, model, opts, initCtx)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		// Weave mid-turn steering into THIS turn before the next round (pi
		// parity): the previous round's assistant+tool messages are appended,
		// so draining steering now appends a user message at the tail and the
		// very next runStreamRound already sees it. Cache-safe (guideline #9):
		// strictly append-only, never a history rewrite.
		a.drainSteeringIntoHistory()
	}
}

// runStreamRound performs one LLM stream round, handling tool calls,
// progress checks, and stream failures. It returns done=true when the turn
// should end after this round (no further tool calls to process).
func (a *Agent) runStreamRound(ctx context.Context, round int, model provider.Model, opts provider.StreamOptions, initCtx provider.Context) (done bool, err error) {
	stream, streamErr := a.startStreamRound(ctx, round, model, opts, initCtx)
	if streamErr != nil {
		// An error opening the stream (e.g. HTTP 408 before any events arrive)
		// is a transient failure like a mid-stream error: route it through the
		// same retry path so the user-visible "goa will retry automatically"
		// hint is actually honored.
		return a.handleRoundStreamError(ctx, streamErr, model, opts)
	}

	toolCallEncountered, streamErr := a.consumeStream(ctx, stream, opts)
	if streamErr != nil {
		return a.handleRoundStreamError(ctx, streamErr, model, opts)
	}

	if !toolCallEncountered {
		a.mu.Lock()
		a.consecutiveToolRounds = 0
		a.mu.Unlock()
		a.noteCleanStreamRound()
		return true, nil
	}

	// Convergence is message-driven, NOT a numeric round cap (Issue 13).
	// trackToolCallingRound reports true only when the model has gone silent for
	// the configured number of consecutive rounds (no message AND no thinking
	// anywhere in the turn). A model still producing messages/reasoning never
	// reaches this and is never cut off by an arbitrary round number — this
	// replaces both the old forced-answer nudge and the hard 250-round cap.
	if a.trackToolCallingRound() {
		reason := fmt.Sprintf("model went silent for %d consecutive tool-only rounds (limit %d)",
			a.silentRoundStreak(), a.effectiveMaxConsecutiveToolRounds())
		if err := a.runRecoveryStream(ctx, model, opts, reason); err != nil {
			return false, err
		}
		return true, nil
	}
	a.noteCleanStreamRound()
	return false, nil
}

// handleRoundStreamError routes a stream-open or mid-stream failure through
// the shared retry classification (handleStreamFailure). It maps the outcome
// onto runStreamRound semantics: done=true when the retry recovered with no
// further tool calls, err set when the failure is terminal.
func (a *Agent) handleRoundStreamError(ctx context.Context, streamErr error, model provider.Model, opts provider.StreamOptions) (done bool, err error) {
	handled, retErr := a.handleStreamFailure(ctx, streamErr, model, opts)
	if !handled {
		return false, nil
	}
	if retErr != nil {
		return false, retErr
	}
	// Retry succeeded and produced no further tool calls: turn is done.
	return true, nil
}

// trackToolCallingRound updates the consecutive tool-calling round streak
// after a round that ended with tool calls. A round that streamed visible
// text (contentBuf non-empty) OR thinking tokens (thinkingBuf non-empty) is
// NOT a silent tool round — the model was actively reasoning — so it resets
// the streak instead of incrementing it. Only tool-call-only rounds with no
// reasoning or visible output count toward the forced-answer hint.
// trackToolCallingRound updates the silent tool-round streak after a round that
// ended with tool calls, and reports whether the model has gone silent for the
// configured number of consecutive rounds (no message AND no thinking anywhere
// in the turn). A round — or any earlier round this turn — that streamed visible
// text or thinking resets the streak: the model is actively reasoning, not stuck.
func (a *Agent) trackToolCallingRound() (silentLimitReached bool) {
	a.mu.Lock()
	hadContent := a.contentBuf.Len() > 0
	hadThinking := a.thinkingBuf.Len() > 0
	hadTurnReasoning := a.turnSawContent || a.turnSawThinking
	a.mu.Unlock()
	if hadContent || hadThinking || hadTurnReasoning {
		a.mu.Lock()
		a.consecutiveToolRounds = 0
		a.mu.Unlock()
		return false
	}
	return a.checkConsecutiveToolRounds()
}

// checkConsecutiveToolRounds increments the consecutive tool-calling round
// counter and, when the configured limit is reached, injects an ephemeral
// system message telling the model to stop calling tools and answer with
// what it has gathered. This catches the "infinite tool-calling loop" where
// every call has unique inputs and existing repeat guardrails never fire.
// checkConsecutiveToolRounds increments the silent tool-round streak and reports
// whether the streak just reached the configured limit. The caller converges
// (recovery stream) at that point. The streak is NOT reset here, so the caller
// can act on the limit in the same round; it resets naturally on the next round
// that produces a message/thinking or no tool call.
func (a *Agent) checkConsecutiveToolRounds() bool {
	a.mu.Lock()
	a.consecutiveToolRounds++
	reached := a.effectiveMaxConsecutiveToolRounds() > 0 &&
		a.consecutiveToolRounds >= a.effectiveMaxConsecutiveToolRounds()
	a.mu.Unlock()
	return reached
}

// startStreamRound builds the provider context and opens a stream.
// On round 0 it uses the initial context from prepareTurn; on subsequent
// rounds it rebuilds from the updated history.  Resets per-round flags
// (streamLoopDetected, contentBuf, thinkingBuf) so a previous round's
// state doesn't poison the re-stream.
func (a *Agent) startStreamRound(ctx context.Context, round int, model provider.Model, opts provider.StreamOptions, initCtx provider.Context) (*provider.AssistantMessageEventStream, error) {
	if round > 0 {
		a.cfg.Logger.Log(Info, "Re-streaming after tool call (round %d)", round)
		a.emitEvent(OutputEvent{Type: EventProgress, Text: "Sending request..."})
		// Per-round compression gate: prepareTurn gates once per user turn,
		// but a long tool-call turn can climb past the trigger/hard ceiling
		// between rounds — a TC:436 session sailed past 100% unchecked until
		// the provider rejected the request (compression entry).
		// Re-check before every re-stream so no request leaves oversized.
		compressErr := a.maybeCompress(ctx)
		if compressErr != nil {
			a.cfg.Logger.Log(Error, "per-round compression failed: %v", compressErr)
		}
		// Same dead-turn guard as prepareTurn: a canceled round must not
		// trigger the destructive ceiling fallback.
		a.enforceContextCeilingUnlessCanceled(ctx, compressionCauseFromErr(compressErr))
		a.mu.Lock()
		a.resetStreamRoundState()
		a.mu.Unlock()
		// Later-step entry: inject the per-turn temporal context reading
		// (CX6) before the re-stream request is derived.
		a.injectTimeContextIfDue(time.Now())
		pCtx := a.buildProviderContext(ctx)
		// Final-step collapse (P7): a pending stop-turn signal marks this
		// round text-only — no tools, tool_choice none — so the model
		// produces its summary instead of calling more tools. The flag is
		// consumed here; the next round (or turn) restores the full set.
		// The stats latch follows the collapse so THIS round's token stats
		// carry TextOnlyCollapse (bugs.md 2026-08-30); a non-collapsed round
		// clears any stale latch instead of leaking it forward.
		if a.toolCollapseNextRound {
			pCtx.NoTools = true
			a.toolCollapseNextRound = false
			a.collapseStatsPending = true
		} else {
			a.collapseStatsPending = false
		}
		return a.stream(model, pCtx, opts)
	}
	a.logProviderContext(initCtx, 0)
	return a.stream(model, initCtx, opts)
}

// effectiveEventStallTimeout returns the maximum wall-clock time the agent
// waits between stream events before declaring the stream stalled. It derives
// from opts.IdleTimeout (which the HTTP layer uses as a byte-level idle guard);
// a zero or negative value falls back to the default idle timeout (2 minutes).
//
// Unlike the byte-level idle timeout — reset by every byte, including SSE
// keep-alive comments (": ping") and empty lines — this timeout is reset only
// by actual stream events (text deltas, thinking deltas, tool calls, etc.).
// This prevents indefinite hangs when a provider sends periodic keep-alive
// bytes but never delivers a meaningful response.
func (a *Agent) effectiveEventStallTimeout(opts provider.StreamOptions) time.Duration {
	if opts.IdleTimeout > 0 {
		return opts.IdleTimeout
	}
	return provider.DefaultStreamIdleTimeout
}

// effectiveMaxStreamRounds returns the configured per-turn stream round
// cap. Zero or negative disables the cap: there is deliberately no hidden
// fallback (matching the MaxConsecutiveToolRounds contract) — the application
// layer supplies any desired default via config/flags.
func (a *Agent) effectiveMaxStreamRounds() int {
	if a.cfg.MaxStreamRounds > 0 {
		return a.cfg.MaxStreamRounds
	}
	return 0
}

// silentRoundStreak snapshots the current silent tool-round streak for
// logging; the limit decision itself lives in trackToolCallingRound.
func (a *Agent) silentRoundStreak() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.consecutiveToolRounds
}

// effectiveMaxConsecutiveToolRounds returns the configured max consecutive
// tool-calling rounds. Non-positive values disable the guardrail; the
// application layer supplies the normal default of 15. The nudge also fires
// at most once per turn (toolRoundNudgeFired).
func (a *Agent) effectiveMaxConsecutiveToolRounds() int {
	// Zero is an explicit disable value. The application config supplies the
	// normal default (15); keeping the agent fallback disabled avoids turning a
	// missing/zero value into a surprising hidden limit.
	if a.cfg.MaxConsecutiveToolRounds <= 0 {
		return 0
	}
	return a.cfg.MaxConsecutiveToolRounds
}

// runRecoveryStream sends a clear system message to the LLM when a per-turn
// convergence bound fires (silent tool-round streak or the explicit
// MaxStreamRounds cap — the caller supplies the reason), then performs a
// final stream so the model can self-heal and produce an answer from
// information already gathered.
//
// If the model ignores the hint and still calls tools, we allow up to
// maxRecoveryRounds additional rounds so the model can see tool results and
// produce a text response. Without this, tool results get silently appended
// to history with no chance for the model to respond, leaving the user with
// no visible output and a seemingly hung session.
func (a *Agent) runRecoveryStream(ctx context.Context, model provider.Model, opts provider.StreamOptions, reason string) error {
	a.cfg.Logger.Log(Warn, "%s; sending recovery hint", reason)
	recovery := "[goa-system] Internal control note (never show or mention to the user): the per-turn tool-call round limit was reached. Complete the task now using the information already gathered, without referencing this note or any internal limit."
	// The recovery hint is a transient nudge for the recovery rounds only; mark
	// it ephemeral so it is stripped at turn end and does not pollute future
	// turns' context.
	a.InjectEphemeralSystemMessage(recovery)

	// Allow up to 3 additional recovery rounds if the model still calls tools
	// despite the recovery hint. Prevents runaway recovery while still giving
	// the model a chance to respond to tool results from earlier rounds.
	const maxRecoveryRounds = 3

	for round := 0; round < maxRecoveryRounds; round++ {
		// Recovery-step entry: inject the per-turn temporal context reading
		// (CX6) before the recovery request is derived.
		a.injectTimeContextIfDue(time.Now())
		pCtx := a.buildProviderContext(ctx)
		// Final-step collapse (P7): recovery rounds are the turn's last
		// step — the model must answer with what it has gathered, so the
		// request carries no tools and tool_choice none. The stats latch
		// marks the recovery round's token stats as a no-tools step
		// (bugs.md 2026-08-30).
		pCtx.NoTools = true
		a.collapseStatsPending = true
		a.logProviderContext(pCtx, round)

		recoveryStream, err := a.stream(model, pCtx, opts)
		if err != nil {
			return fmt.Errorf("recovery stream: %w", err)
		}

		toolCallEncountered, streamErr := a.consumeStream(ctx, recoveryStream, opts)
		if streamErr != nil {
			if handled, retErr := a.handleStreamFailure(ctx, streamErr, model, opts); handled {
				return retErr
			}
			return streamErr
		}

		if !toolCallEncountered {
			return nil
		}

		a.cfg.Logger.Log(Warn, "recovery round %d: model still called tools, retrying", round)
	}

	a.cfg.Logger.Log(Warn, "recovery stream exhausted all %d rounds; ending turn", maxRecoveryRounds)
	// Surface a visible notification so the user is not left wondering why
	// processing stopped. The model ignored the recovery hint and kept calling
	// tools; without this the turn ends silently (spinner clears, no output).
	a.emitEvent(OutputEvent{
		Type:  EventContent,
		State: StateContent,
		Role:  Assistant,
		Text:  "[goa-system] The model was stuck in a tool-calling loop and could not produce an answer after a recovery hint. Try rephrasing or asking a more specific question.",
	})
	return nil
}

// prepareTurn resets per-turn state, applies proactive compression, and builds
// the initial provider context and request options.

func (a *Agent) consumeStream(ctx context.Context, stream *provider.AssistantMessageEventStream, opts provider.StreamOptions) (bool, error) {
	a.genStartTime = time.Time{} // reset per stream; window opens below
	a.genSawEvent = false        // reset per stream; set on first mapped event
	a.startGenTiming()           // time from stream start, not first mapped event

	// Event-level stall watchdog: unlike the byte-level idle timeout in the
	// HTTP reader — which is reset by every byte, including SSE keep-alive
	// comments (": ping") and empty lines — this timer resets ONLY on actual
	// stream events (text deltas, thinking deltas, tool calls, etc.). If the
	// provider sends keep-alive bytes but never delivers a real event, the
	// byte-level idle timeout never fires and the agent hangs indefinitely.
	// The watchdog terminates the stream with a stall error, which is then
	// handled by handleStreamFailure (transient, retryable).
	stallTimeout := a.effectiveEventStallTimeout(opts)
	watchdog := time.AfterFunc(stallTimeout, func() {
		a.cfg.Logger.Log(Warn, "Stream stalled: no events received for %v", stallTimeout)
		stream.CloseWithError(fmt.Errorf("stream stalled: no events received from provider for %v", stallTimeout))
	})
	defer watchdog.Stop()

	for event := range stream.SeqCtx(ctx) {
		// An event arrived — the provider is alive. Push the stall deadline out.
		watchdog.Reset(stallTimeout)

		if err := ctx.Err(); err != nil {
			return false, err
		}

		done, toolCallsEncountered, err := a.handleStreamEvent(ctx, stream, event)
		if done {
			return toolCallsEncountered, err
		}
	}

	return a.finishStreamTurn(ctx, stream)
}

// handleStreamEvent dispatches a single stream event. The returned done flag is
// true when the stream has reached a terminal state (success or error).
//
// streamLoopDetected is checked BEFORE the handler dispatch. checkStreamLoop
// (called inside handleTextDelta/handleThinkingDelta) sets the flag when the
// model starts repeating the same text suffix within a single response.
// Previously the flag was only consulted in the else branch (unknown event
// types), which never carry the looped text deltas — so the detection was dead
// code for the exact event types that needed it, and repeated text streamed
// through unchecked, producing visible duplication in the message bubble.
func (a *Agent) handleStreamEvent(ctx context.Context, stream *provider.AssistantMessageEventStream, event provider.AssistantMessageEvent) (done bool, toolCallsEncountered bool, err error) {
	if a.streamLoopDetected {
		a.streamLoopDetected = false // consumed here; per-round state resets before the next round
		toolCallsEncountered, err := a.handleStreamLoopStrike(ctx)
		return true, toolCallsEncountered, err
	}
	// The thinking-stall watchdog is a separate guard from the stream loop
	// detector: it fires when the model emits only reasoning tokens for too
	// long, never because of repeated text. It previously reused the
	// streamLoopDetected flag and its "stream loop" error, so turns killed
	// by the watchdog were misreported and could not be disabled via the
	// stream-loop toggle. Report it under its own name; it is tuned and
	// disabled independently (execution.thinking_stall_stop_seconds,
	// execution.disable_thinking_stall_detection).
	if a.thinkingStalled {
		a.cfg.Logger.Log(Warn, "Stopping stream: thinking stalled for %v without content or tool calls", a.thinkingStallElapsed)
		return true, false, fmt.Errorf("thinking stalled: the model produced only reasoning output for %v without reply text or tool calls; turn stopped (tune execution.thinking_stall_stop_seconds or disable via /config:temp:thinking_stall_detection:off)", a.thinkingStallElapsed.Round(time.Second))
	}
	if handler, ok := streamEventHandlers[event.Type]; ok {
		return handler(a, ctx, stream, event)
	}
	return false, false, nil
}
