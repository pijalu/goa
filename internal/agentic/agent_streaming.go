// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"
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
		if a.toolCollapseNextRound {
			pCtx.NoTools = true
			a.toolCollapseNextRound = false
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
		// request carries no tools and tool_choice none.
		pCtx.NoTools = true
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

// streamEventHandler processes a single stream event and reports whether the
// stream has reached a terminal state.
// no recourse. That is not the "malformed local model" case the opt-in exists
// for; it is a first-class provider format and must never be refused.
//
// The generic <tool_call>/<function=name> forms remain gated behind
// AutoHealToolCalls (opt-in for weak local models). Discovered calls are run
// through the ToolLoopController and either buffered for execution or recorded
// as no-ops with a nudge message. It returns true when at least one call was
// discovered.
func (a *Agent) completeStreamTurn(ctx context.Context) bool {
	// Flush any cache-miss notice that landed after the last per-round drain
	// (the journal attaches usage just after the stream closes).
	a.drainCacheMissNotices()
	// Window-edge truncation (finish_reason=length at ~100% of the window) is
	// the last warning before a provider-side context rejection on the next
	// request: compress before any continuation round goes out.
	a.maybeCompressAfterLengthTruncation()

	if a.tryAutoHealToolCalls() {
		// fall through to tool execution below
	}

	hasToolCalls := len(a.bufferedToolCalls) > 0

	if hasToolCalls {
		// Tool calls present: build the full assistant message (content + tool
		// calls) inside executeBufferedToolCalls, then emit end events.
		// If every call was a budget placeholder, there is no new real result
		// to send back to the model, so the turn ends here.
		hadRealExecution := a.executeBufferedToolCalls(ctx)
		a.emitTurnStats()
		a.checkSilentOverflow()
		// Decide whether the loop will stream another round. The turn continues
		// only when a real tool executed. A stop-turn signal (StopTurn tool
		// result) does not end the turn outright: it stops the current tool
		// batch and marks the next round text-only, so the model's summary
		// response comes immediately without further tool calls (P7, TC6).
		// EventEnd is emitted exclusively on the finishing path so mid-turn UI
		// consumers never observe a premature turn end (which previously dropped
		// the status spinner after the first tool call).
		if a.stopBatchAfterThis {
			a.stopBatchAfterThis = false
			a.toolCollapseNextRound = true
		}
		turnContinues := hadRealExecution
		if !turnContinues {
			a.emitEvent(OutputEvent{Type: EventEnd})
		}
		return turnContinues
	}

	// No tool calls: check for a premature stop before finalizing. Some
	// providers (deepseek/opencode-go) emit finish_reason=stop mid-task after
	// a long tool-work streak, truncating the reply mid-sentence. When the
	// turn did real tool work and the final answer is clearly incomplete,
	// auto-continue with a steer instead of ending the turn — the user should
	// not have to type "continue".
	if a.shouldAutoContinue() {
		a.autoContinueCount++
		a.cfg.Logger.Log(Warn, "premature stop detected (incomplete output after tool work); auto-continuing turn (attempt %d/%d)", a.autoContinueCount, maxAutoContinuePerTurn)
		a.InjectEphemeralSystemMessage(
			"[goa-system] Internal control note (never show or mention to the user): your previous reply was cut " +
				"off mid-task before completion. Continue the work immediately and finish it now. Do not restart, " +
				"do not re-summarize, and do not stop until the task is fully done.")
		return true // treat like a continuing round; re-stream
	}

	// No tool calls: finalizeTurn appends the message and emits end events.
	a.finalizeStreamTurn()
	return false
}

// maxAutoContinuePerTurn bounds how many times a turn may auto-continue after
// a detected premature stop, so a provider that keeps truncating cannot loop.
func (a *Agent) finishStreamTurn(ctx context.Context, stream *provider.AssistantMessageEventStream) (bool, error) {
	// A loop detected by the very last delta has no following event to trip
	// the dispatch-level check, so handle it here as well.
	if a.streamLoopDetected {
		a.streamLoopDetected = false
		return a.handleStreamLoopStrike(ctx)
	}
	// If the stream terminated with an error, surface it before finalizing.
	// Context-length errors are handled with compression; other errors are
	// passed to handleStreamFailure for retry.
	if err := stream.Err(); err != nil {
		a.recordGenDuration()
		if isContextLengthError(err) {
			// Check for context overflow BEFORE finalizing the turn.  If the stream
			// terminated with a context-length error, we must NOT call finalizeStreamTurn
			// because that would emit EventEnd (telling the UI the turn is done) and
			// append partial content to history.  The retry would produce a second
			// EventEnd, and the UI would see two turns — the duplicate response bug.
			// Instead, skip finalization: let the error propagate to handleStreamFailure
			// which will undo any partial assistant message, compress, and retry.
			a.handleContextError(ctx, err)
			return false, err
		}
		return false, err
	}

	// Extract provider Usage from the stream result (set by updateResultWithUsage
	// after the usage chunk arrives from stream_options.include_usage).
	a.captureStreamResult(stream)
	a.recordGenDuration()

	// Empty-response guard: a clean stream end (2xx + [DONE]/EOF) that emitted
	// no stream events at all (no text/thinking/tool-call deltas — genSawEvent
	// is false) is not a legitimate answer when the model has done no tool
	// work this turn. Under provider load it indicates a truncated/failed
	// response, so it is routed through handleStreamFailure (bounded retry,
	// then a surfaced message) instead of ending the turn silently. It is
	// scoped to turns with no real tool execution: after a tool runs and its
	// result is sent back, an empty follow-up is a legitimate "done, nothing
	// more to say" turn end. A stream that emitted events but produced empty
	// text (e.g. loop-detector fixtures) sets genSawEvent and is NOT treated
	// as empty here; thinking-only turns are handled by the silent-stop notice
	// in finalizeStreamTurn.
	if !a.cfg.AllowEmptyResponse && !a.turnHadToolExecution && !a.genSawEvent && len(a.bufferedToolCalls) == 0 {
		return false, errEmptyResponse
	}

	toolCallsEncountered := a.completeStreamTurn(ctx)
	return toolCallsEncountered, nil
}

// resolveStreamError extracts the error from a stream error event.
func (a *Agent) resolveStreamError(ctx context.Context, stream *provider.AssistantMessageEventStream, eventErr error) error {
	// Detect context overflow BEFORE finalizing the turn so the
	// duplicate-EventEnd bug is avoided.  Check both eventErr and
	// stream.Err() since the error may be in either location.
	err := eventErr
	if err == nil {
		err = stream.Err()
	}
	if err != nil && isContextLengthError(err) {
		a.handleContextError(ctx, err)
		return err
	}

	// For non-context errors, return the error so handleStreamFailure can retry.
	// Do NOT finalize the turn here: doing so would emit a spurious EventEnd and
	// append a partial assistant message that would be left behind after the
	// retry succeeds, producing duplicate responses in the UI.
	if e := stream.Err(); e != nil {
		a.cfg.Logger.Log(Error, "stream error: %v", e)
		return e
	}
	if eventErr != nil {
		a.cfg.Logger.Log(Error, "stream error: %v", eventErr)
		return eventErr
	}
	a.cfg.Logger.Log(Warn, "stream ended with error event but no error object")
	return fmt.Errorf("LLM stream disconnected unexpectedly")
}

// finalizeStreamTurn appends the assistant buffer to history and emits EventEnd.
func (a *Agent) finalizeStreamTurn() {
	msg := a.synthesizeAssistantBuffer()
	a.mu.Lock()
	a.history = append(a.history, msg)
	a.mu.Unlock()

	// Silent-stop guard: a reasoning model (e.g. one that streams
	// reasoning_content) can finish with finish_reason "stop" after emitting
	// only thinking tokens — no visible answer, no tool calls. Without a
	// notice the user sees the thinking block collapse and the spinner clear
	// with no explanation ("session stopped without any message"). When the
	// turn produced no visible answer content, surface a non-transient system
	// message so the stop is never silent. The thinking is still preserved in
	// history (synthesizeAssistantBuffer promotes it to content), so a
	// follow-up "continue" resumes with full context.
	if a.contentBuf.Len() == 0 && a.thinkingBuf.Len() > 0 {
		a.cfg.Logger.Log(Warn, "turn ended with thinking but no answer content (model stopped mid-reasoning)")
		a.mu.Lock()
		a.lastTurnSilentStop = true
		a.mu.Unlock()
		a.emitEvent(OutputEvent{
			Type: EventContent,
			Role: System,
			Text: "The model stopped after its reasoning step without producing a reply " +
				"(no answer text or tool calls were returned). This is usually a " +
				"reasoning-token or output limit on the provider side. Send \"continue\" " +
				"to resume, or rephrase your request.",
			Metadata: map[string]string{"category": "system-notification"},
		})
	}

	// Emit token/context stats before EventEnd so consumers can log/use them
	// when the turn officially completes.
	a.emitTurnStats()
	a.checkSilentOverflow()
	a.emitEvent(OutputEvent{Type: EventEnd})
}

func (a *Agent) resetStreamRoundState() {
	a.contentBuf.Reset()
	a.thinkingBuf.Reset()
	a.thinkingDisplayBuf.Reset()
	a.bufferedToolCalls = nil
	a.bufferedToolCallCount = 0
	a.streamLoopDetected = false
	a.streamLoopSample = ""
	a.streamLoopStrikeThisRound = false
	a.thinkingStalled = false
	a.thinkingStallElapsed = 0
	a.resetThinkingStallLocked()
	a.streamingToolCalls = nil
	a.streamingToolCallsByIndex = nil
	a.toolCallDeltasThisRound = 0
}

// checkStreamLoop detects immediate repetition of a suffix within the current
// streaming buffer. If the buffer ends with the same meaningful substring
// repeated consecutively, the model is likely stuck in a loop; set
// streamLoopDetected so the turn can be stopped quickly.
//
// To reduce false positives:
//   - Text is normalized to letters, digits, and spaces only
//   - Only triggers on sufficiently large content
//   - Requires the repeated pattern to span at least two unique words
func (a *Agent) checkStreamLoop(text string) {
	// Detection can be disabled per session (/config:temp:stream_loop_detection:off)
	// or persistently (execution.disable_stream_loop_detection); checked per
	// delta so a mid-stream toggle takes effect immediately.
	if a.cfg.StreamLoopDisabled != nil && a.cfg.StreamLoopDisabled() {
		return
	}
	// Normalize: strip punctuation, symbols, box-drawing chars, collapse spaces
	clean := streamLoopNormalize(text)
	if period, repeats, sample, ok := streamLoopScan(clean, a.streamLoopMaxRepeats(), a.streamLoopMinPeriod()); ok {
		a.streamLoopDetected = true
		// Keep the repeated sequence as evidence so the strike warning/stop
		// messages can show WHAT was judged a loop (runaway-loop
		// visibility).
		a.streamLoopSample = sample
		a.cfg.Logger.Log(Warn, "Stream loop detected: %d-byte period repeated %d times", period, repeats)
	}
}

// streamLoopMaxRepeats resolves the repeat threshold for the loop detector:
// the user-configured value (execution.stream_loop_max_repeats via the live
// loop detector) when set, otherwise the default of 5.
func (a *Agent) streamLoopMaxRepeats() int {
	const defaultMaxRepeats = 5
	if a.cfg.StreamLoopMaxRepeats == nil {
		return defaultMaxRepeats
	}
	if n := a.cfg.StreamLoopMaxRepeats(); n >= 2 {
		return n
	}
	return defaultMaxRepeats
}

// streamLoopMinPeriod resolves the smallest repeated unit Detector A treats
// as a loop: the user-configured value (execution.stream_loop_min_period via
// the live loop detector) when set, otherwise the built-in default. Values
// below the absolute scan floor (streamLoopSmallPeriod) fall back to the
// default because such periods are never scanned.
func (a *Agent) streamLoopMinPeriod() int {
	if a.cfg.StreamLoopMinPeriod != nil {
		if n := a.cfg.StreamLoopMinPeriod(); n >= streamLoopSmallPeriod {
			return n
		}
	}
	return streamLoopExactMinPeriod
}

func (a *Agent) prepareTurn(ctx context.Context) (provider.Model, provider.StreamOptions, provider.Context) {
	a.mu.Lock()
	a.turnToolCalls = make(map[string]int)
	a.turnToolCallCount = 0
	a.turnHadToolExecution = false
	a.turnSawContent = false
	a.turnSawThinking = false
	a.lastTurnSilentStop = false
	a.contentBuf.Reset()
	a.thinkingBuf.Reset()
	a.thinkingDisplayBuf.Reset()
	a.contentDisplayBuf.Reset()
	a.contentMarkupSeen = false
	a.turnStatsEmitted = false
	a.turnStartHistoryLen = len(a.history)
	a.bufferedToolCalls = nil
	a.bufferedToolCallCount = 0
	a.budgetToolCalls = make(map[string]string)
	a.stopBatchAfterThis = false
	a.toolCollapseNextRound = false
	a.providerUsage = nil
	a.recentToolCalls = nil
	a.lastCallKey = ""
	a.consecutiveCount = 0
	a.stateEpoch = 0
	a.epochAtLastCall = 0
	a.errStreakTool = ""
	a.errStreak = 0
	a.errStreakNudged = false
	a.streamLoopDetected = false
	// The strike counter itself is session-scoped (it decays only via clean
	// activity, per execution.stream_loop_reset_after); only the per-round
	// marker resets here.
	a.streamLoopStrikeThisRound = false
	a.overflowRecoveryAttempted = false
	a.consecutiveToolRounds = 0
	a.toolRoundNudgeFired = false
	a.autoContinueCount = 0
	a.lastStopReason = ""
	a.turnStep = 0
	a.mu.Unlock()

	compressErr := a.maybeCompress(ctx)
	if compressErr != nil {
		a.cfg.Logger.Log(Error, "proactive compression failed: %v", compressErr)
	}
	// Never cut history for a turn that is already dead: the destructive
	// ceiling fallback exists to let the NEXT request go out, and a canceled
	// context means no request will — dropping messages would be silent data
	// loss. The cause threads the real summarize outcome so a false "did not
	// fit the window" is never reported.
	a.enforceContextCeilingUnlessCanceled(ctx, compressionCauseFromErr(compressErr))

	// Step 1 entry: inject the per-turn temporal context reading (CX6) before
	// the first provider request of the turn is derived.
	a.injectTimeContextIfDue(time.Now())

	pCtx := a.buildProviderContext(ctx)

	model := a.cfg.Model
	if a.cfg.ToolResultAsUser != nil {
		model = a.withToolResultAsUser(model, *a.cfg.ToolResultAsUser)
	}

	opts := a.cfg.StreamOptions
	if opts.APIKey == "" && a.cfg.APIKey != "" {
		opts.APIKey = a.cfg.APIKey
	}
	// Apply provider-level defaults (timeout, idle timeout, retries, transport,
	// cache retention) so the agent always passes a fully-resolved options struct
	// to provider.Stream. Without this, a Config that leaves StreamOptions zero
	// would get MaxRetries=0 and the stream retry loop would never run.
	opts = provider.BuildBaseOptions(model, opts)

	return model, opts, pCtx
}

// formatRetryMessage and formatFatalStreamMessage now live in retry_classify.go,
// alongside the retry-decision helpers (shouldRetryStreamError / retryBackoff).

// handleStreamFailure handles a stream error, retrying when appropriate.
// Returns true if the failure was fully handled (caller should return retErr).
func (a *Agent) stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	key := a.cacheKey(model)
	opts.PromptCacheKey = key
	a.mu.Lock()
	a.activeCacheKey = key
	a.mu.Unlock()
	return provider.Stream(model, ctx, opts)
}

func (a *Agent) buildProviderContext(ctx context.Context) provider.Context {
	msgs := a.buildProviderHistory()
	sp := a.buildSystemPrompt()
	msgs = mergeGoalProgress(msgs, a.cfg.GoalStateProvider)

	return provider.Context{
		Context:      ctx,
		SystemPrompt: sp,
		Messages:     msgs,
		Tools:        migrateSchemas(a.reg.Schemas(), a.cfg.Model),
	}
}

func (a *Agent) buildProviderHistory() []provider.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	internal := make([]Message, 0, len(a.history))
	for i, m := range a.history {
		// Skip only the initial system prompt message; the provider context
		// carries it separately via SystemPrompt. Later system messages (for
		// example runtime tool-change notifications) must still be sent.
		if i == 0 && a.cfg.SystemPrompt != "" && m.Role == System {
			continue
		}
		// Downgrade any non-leading system message to a user-role context
		// note. Runtime injections (context-reset boundary, goal/swarm
		// reminders, ephemeral control nudges) are appended with Role
		// System, but a system message after the first is not portable:
		// strict Jinja chat templates (LM Studio/llama.cpp) reject the
		// whole request with HTTP 400 "System message must be at the
		// beginning", and the Anthropic/Google/Mistral/Bedrock serializers
		// silently drop it. Sending it as user keeps the notice visible to
		// the model on every provider — the same reason persistGoalReminder
		// is user-role. A leading system message (i == 0, no separate
		// SystemPrompt) stays system: it IS at the beginning, so strict
		// templates accept it.
		if i > 0 && m.Role == System {
			m.Role = User
		}
		internal = append(internal, m)
	}
	// Route through migrateMessages (not per-message migrateMessage) so elided
	// tool calls are converted to text notes WITH their matching tool results
	// dropped — per-message conversion alone would orphan the results and
	// break call/result pairing on strict providers.
	return migrateMessages(internal)
}

func (a *Agent) buildSystemPrompt() string {
	// The system prompt is the provider-cached prefix: it must stay
	// byte-identical across the whole session, including goal create/destroy/
	// status-flips. Goal text therefore does NOT belong here — it is injected
	// as volatile slot messages by mergeGoalProgress instead
	// "CRITICAL: /goal destroy caching": a goal reminder in this prefix busted
	// the entire prompt cache on every goal transition).
	return a.cfg.SystemPrompt
}

// persistGoalReminder appends the current goal context to a.history as
// ordinary USER-role messages, once per turn (called from runInternal right
// after the user message). kimi-code parity (systemReminderService.ts):
// the reminder becomes part of the append-only conversation, so every
// provider request is a strict append of the previous one — the whole prefix
// (system prompt + full history, reminders included) is served from the
// provider prompt cache and NOTHING is re-sent per round.
//
// Two messages are appended, in order: the static reminder (objective,
// completion criterion, status notes) and the dynamic progress snapshot
// (turn/token/elapsed counters frozen at turn start). They are NEVER system
// role: strict chat templates (LM Studio/llama.cpp Jinja) reject any system
// message after the first with HTTP 400 "System message must be at the
// beginning".
//
// Trade-offs (decided 2026-07-21): reminders persist, so a
// cancelled goal's text stays in history — accepted per kimi-code; the
// engine already appends an explicit "Goal cancelled/paused" history note on
// transitions, which supersedes them. Progress counters are per-turn fresh
// (a new snapshot each turn), never mid-turn.
//
// Concurrency: caller holds no lock (same as the user-message append);
// emitMessage is invoked outside a.mu like the neighboring call.
func (a *Agent) persistGoalReminder() {
	p := a.cfg.GoalStateProvider
	if p == nil {
		return
	}
	if reminder := p.ActiveGoalReminder(); reminder != "" {
		// E5 (ENHANCE.md): the static reminder is byte-identical for a given
		// goal across turns, so re-appending it every turn only bloats the
		// append-only context (~1.5KB of guidance per turn in a long goal
		// session). Persist it once, and again only when it actually changes
		// (new goal, edited objective, status flip). The dynamic progress below
		// legitimately churns and keeps updating each turn.
		if reminder != a.lastPersistedGoalReminder {
			msg := Message{Type: Content, Role: User, Content: "[goal]\n" + reminder}
			a.history = append(a.history, msg)
			a.emitMessage(msg)
			a.lastPersistedGoalReminder = reminder
		}
	}
	if progress := p.ActiveGoalProgress(); progress != "" {
		msg := Message{Type: Content, Role: User, Content: "[goal progress]\n" + progress}
		a.history = append(a.history, msg)
		a.emitMessage(msg)
	}
}

// mergeGoalProgress is a passthrough: goal context is persisted once per
// turn into a.history by persistGoalReminder (kimi-code parity), so there is
// nothing to merge per request — the history already carries it and the
// request stays a strict append of the previous one.
func mergeGoalProgress(msgs []provider.Message, p GoalStateProvider) []provider.Message {
	return msgs
}

// deliverPreTurnMessages appends the PreTurnProvider's claimed user-role
// content to history ahead of the current turn's user message. The provider is
// expected to consume what it returns (the schedule store claims due jobs
// atomically), so a provider that returns content is only invoked once per
// delivery — a second call in the same turn returns nothing.
//
// Concurrency: caller holds no lock (same as the user-message append);
// emitMessage is invoked outside a.mu like the neighboring call.
func (a *Agent) deliverPreTurnMessages() {
	p := a.cfg.PreTurnProvider
	if p == nil {
		return
	}
	for _, text := range p.PreTurnMessages() {
		if text == "" {
			continue
		}
		msg := Message{Type: Content, Role: User, Content: text}
		a.history = append(a.history, msg)
		a.emitMessage(msg)
	}
}

// persistStickyInstructions appends always-on instruction blocks (sticky
// knowledge skills) to a.history as ordinary USER-role messages, using the
// same kimi-code parity contract as persistGoalReminder: append-only history,
// provider-cache-friendly, never system role (strict chat templates reject
// mid-conversation system messages with HTTP 400; Anthropic/Google
// serializers silently drop them).
//
// All blocks are joined into a single message and deduped by string compare:
// re-appending is skipped while the sticky set is unchanged. Compression
// passes call InvalidateStickyInstructions after mutating history so the
// next turn re-persists the block when elision/selective/summarize dropped
// it.
func (a *Agent) persistStickyInstructions() {
	p := a.cfg.StickyProvider
	if p == nil {
		return
	}
	joined := strings.Join(p.StickyInstructions(), "\n\n")
	if joined == "" {
		return
	}
	// Dedup against ACTUAL history, not just in-memory state: the append-only
	// contract means a sticky block present anywhere in the conversation is
	// still in effect (elision only mutates tool payloads, and selective/
	// summarize/ceiling all reset lastPersistedSticky via emitCompaction).
	// Scanning also covers session restore (SetHistory), where a restored
	// history may already carry the identical block.
	if a.lastPersistedSticky == joined || historyContains(a.history, joined) {
		a.lastPersistedSticky = joined
		return
	}
	msg := Message{Type: Content, Role: User, Content: "[sticky instructions — always active]\n" + joined}
	a.history = append(a.history, msg)
	a.emitMessage(msg)
	a.lastPersistedSticky = joined
}

// historyContains reports whether any history message's content contains
// the given text. Used for sticky-instruction dedup.
func historyContains(history []Message, text string) bool {
	for _, m := range history {
		if strings.Contains(m.Content, text) {
			return true
		}
	}
	return false
}

// InvalidateStickyInstructions resets the sticky dedup state so the next
// turn re-persists the sticky blocks. Every compression pass that mutates
// history (elision, selective, summarize, ceiling, overflow, truncation)
// must call this — the previously persisted sticky message may have been
// elided or dropped, and sticky skills must survive context compression.
func (a *Agent) InvalidateStickyInstructions() {
	a.lastPersistedSticky = ""
}

// StickyBlocks returns the sticky instruction blocks this agent was
// configured with, or nil when no StickyProvider is set. Exposed for wiring
// verification (pool propagation tests) and diagnostics.
func (a *Agent) StickyBlocks() []string {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.cfg.StickyProvider == nil {
		return nil
	}
	return a.cfg.StickyProvider.StickyInstructions()
}

// logProviderContext writes a concise summary of the context to the debug log.
// This makes it possible to verify that tool calls and tool results are being
// passed back to the LLM correctly.
func (a *Agent) logProviderContext(ctx provider.Context, attempt int) {
	a.cfg.Logger.Log(Debug, "Provider context (attempt %d): %d messages", attempt, len(ctx.Messages))
	for i, m := range ctx.Messages {
		a.logProviderMessage(i, m)
	}
}

func (a *Agent) logProviderMessage(i int, m provider.Message) {
	switch m.Role {
	case provider.RoleAssistant:
		toolCount := countToolCallBlocks(m.Content)
		a.cfg.Logger.Log(Debug, "  [%d] assistant content=%q tool_calls=%d", i, extractTextFromBlocks(m.Content), toolCount)
	case provider.RoleToolResult:
		toolID, toolName := extractToolResultIdentity(m.Content)
		a.cfg.Logger.Log(Debug, "  [%d] tool_result id=%s name=%s text_len=%d", i, toolID, toolName, len(extractTextFromBlocks(m.Content)))
	case provider.RoleUser:
		a.cfg.Logger.Log(Debug, "  [%d] user content_len=%d", i, len(extractTextFromBlocks(m.Content)))
	}
}
