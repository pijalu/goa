// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"
	"time"
	"unicode"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func (a *Agent) processTurnWithStream(ctx context.Context) error {
	a.cfg.Logger.Log(Debug, "Agent.processTurnWithStream started")
	// Strip transient (ephemeral) system nudges at turn end so recovery/repeat
	// hints inform the model during this turn but do not pollute future turns.
	defer a.stripEphemeralSystemMessages()

	model, opts, initCtx := a.prepareTurn(ctx)
	if err := a.checkContextLimit(); err != nil {
		return err
	}

	maxStreams := a.effectiveMaxStreamRounds()

	for round := 0; ; round++ {
		if err := ctx.Err(); err != nil {
			return err
		}
		done, err := a.runStreamRound(ctx, round, model, opts, initCtx, &maxStreams)
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
func (a *Agent) runStreamRound(ctx context.Context, round int, model provider.Model, opts provider.StreamOptions, initCtx provider.Context, maxStreams *int) (done bool, err error) {
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

	// Convergence is message-driven, NOT a numeric round cap (bugs.md Issue 13).
	// trackToolCallingRound reports true only when the model has gone silent for
	// the configured number of consecutive rounds (no message AND no thinking
	// anywhere in the turn). A model still producing messages/reasoning never
	// reaches this and is never cut off by an arbitrary round number — this
	// replaces both the old forced-answer nudge and the hard 250-round cap.
	if a.trackToolCallingRound() {
		if err := a.runRecoveryStream(ctx, model, opts, *maxStreams); err != nil {
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
		// the provider rejected the request (bugs.md compression entry).
		// Re-check before every re-stream so no request leaves oversized.
		if err := a.maybeCompress(ctx); err != nil {
			a.cfg.Logger.Log(Error, "per-round compression failed: %v", err)
		}
		a.enforceContextCeiling()
		a.mu.Lock()
		a.resetStreamRoundState()
		a.mu.Unlock()
		return provider.Stream(model, a.buildProviderContext(ctx), opts)
	}
	a.logProviderContext(initCtx, 0)
	return provider.Stream(model, initCtx, opts)
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

// effectiveMaxStreamRounds returns the configured max stream rounds, defaulting to 50.
func (a *Agent) effectiveMaxStreamRounds() int {
	if a.cfg.MaxStreamRounds > 0 {
		return a.cfg.MaxStreamRounds
	}
	return 50
}

// effectiveMaxConsecutiveToolRounds returns the configured max consecutive
// tool-calling rounds, defaulting to 15. A value of 0 disables the guardrail.
// The default is deliberately higher than a typical investigation's round
// count so legitimate multi-round work (codebase archaeology) is not
// interrupted; the nudge also fires at most once per turn (toolRoundNudgeFired).
func (a *Agent) effectiveMaxConsecutiveToolRounds() int {
	if a.cfg.MaxConsecutiveToolRounds > 0 {
		return a.cfg.MaxConsecutiveToolRounds
	}
	if a.cfg.MaxConsecutiveToolRounds < 0 {
		return 0 // explicitly disabled
	}
	return 15
}

// runRecoveryStream sends a clear system message to the LLM when the per-turn
// stream round limit is reached, then performs one final stream so the model
// can self-heal and produce an answer from information already gathered.
//
// If the model ignores the hint and still calls tools, we allow up to
// maxRecoveryRounds additional rounds so the model can see tool results and
// produce a text response. Without this, tool results get silently appended
// to history with no chance for the model to respond, leaving the user with
// no visible output and a seemingly hung session.
func (a *Agent) runRecoveryStream(ctx context.Context, model provider.Model, opts provider.StreamOptions, limit int) error {
	a.cfg.Logger.Log(Warn, "per-turn stream round limit (%d) reached; sending recovery hint", limit)
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
		pCtx := a.buildProviderContext(ctx)
		a.logProviderContext(pCtx, limit+1+round)

		recoveryStream, err := provider.Stream(model, pCtx, opts)
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

// hasStalled reports whether the model has stopped making progress in the
// current turn. It checks whether any tool call in the most recent batch
// was actually executed (not budget-exceeded, repeated, or looped). A model
// that keeps calling the same tool with the same arguments, or whose calls
// are all budget-exceeded, has stalled.
func (a *Agent) hasStalled() bool {
	a.mu.Lock()
	defer a.mu.Unlock()

	// If no buffered calls at all, we can't judge progress from them,
	// but if real tools were executed this round the model is not stalled.
	if len(a.bufferedToolCalls) == 0 {
		return !a.turnHadToolExecution
	}

	// If any buffered call was NOT in budgetToolCalls, it was executed
	// for real — the model is making progress.
	for _, tc := range a.bufferedToolCalls {
		if _, skipped := a.budgetToolCalls[tc.ToolCallID]; !skipped {
			return false
		}
	}

	// All calls were budget-skipped, repeated, or looped — stalled.
	return true
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
type streamEventHandler func(*Agent, context.Context, *provider.AssistantMessageEventStream, provider.AssistantMessageEvent) (done bool, toolCallsEncountered bool, err error)

var streamEventHandlers = map[provider.EventType]streamEventHandler{
	provider.EventTextDelta:     (*Agent).handleStreamTextDelta,
	provider.EventThinkingDelta: (*Agent).handleStreamThinkingDelta,
	provider.EventToolCallEnd:   (*Agent).handleStreamToolCallEnd,
	provider.EventToolCallStart: (*Agent).handleStreamToolCallStart,
	provider.EventToolCallDelta: (*Agent).handleStreamToolCallDelta,
	provider.EventDone:          (*Agent).handleStreamDone,
	provider.EventError:         (*Agent).handleStreamError,
}

func (a *Agent) handleStreamTextDelta(_ context.Context, _ *provider.AssistantMessageEventStream, event provider.AssistantMessageEvent) (bool, bool, error) {
	a.markGenStart()
	a.handleTextDelta(event)
	return false, false, nil
}

func (a *Agent) handleStreamThinkingDelta(_ context.Context, _ *provider.AssistantMessageEventStream, event provider.AssistantMessageEvent) (bool, bool, error) {
	a.markGenStart()
	a.handleThinkingDelta(event)
	return false, false, nil
}

func (a *Agent) handleStreamToolCallEnd(_ context.Context, _ *provider.AssistantMessageEventStream, event provider.AssistantMessageEvent) (bool, bool, error) {
	if event.ToolCall == nil {
		return false, false, nil
	}
	a.markGenStart()
	a.resetThinkingStall()
	a.shouldBufferToolCall(*event.ToolCall)
	return false, false, nil
}

func (a *Agent) handleStreamToolCallStart(_ context.Context, _ *provider.AssistantMessageEventStream, event provider.AssistantMessageEvent) (bool, bool, error) {
	if event.Partial == nil || len(event.Partial.Content) == 0 {
		return false, false, nil
	}
	a.markGenStart()
	a.resetThinkingStall()
	a.handleToolCallPartial(event.Partial.Content[0], event.ContentIndex)
	return false, false, nil
}

func (a *Agent) handleStreamToolCallDelta(_ context.Context, _ *provider.AssistantMessageEventStream, event provider.AssistantMessageEvent) (bool, bool, error) {
	a.mu.Lock()
	a.toolCallDeltasThisRound++
	a.mu.Unlock()
	// OpenAI-style delta: a full Partial snapshot is attached.
	if event.Partial != nil && len(event.Partial.Content) > 0 {
		a.markGenStart()
		a.resetThinkingStall()
		a.handleToolCallPartial(event.Partial.Content[0], event.ContentIndex)
		return false, false, nil
	}
	// Anthropic-style delta: Partial is nil but Delta carries incremental JSON,
	// correlated by ContentIndex. Without this the streamed arguments never
	// reach the TUI until the whole call completes.
	if event.Delta != "" {
		a.markGenStart()
		a.resetThinkingStall()
		a.handleToolCallDeltaByIndex(event.ContentIndex, event.Delta)
	}
	return false, false, nil
}

func (a *Agent) handleStreamDone(ctx context.Context, stream *provider.AssistantMessageEventStream, _ provider.AssistantMessageEvent) (bool, bool, error) {
	// P0 diagnostic: record whether this provider streamed tool-call args at
	// all. A zero count means tool widgets can only appear at call completion
	// (no live arg streaming) for this provider/model combination.
	a.mu.Lock()
	deltas := a.toolCallDeltasThisRound
	a.mu.Unlock()
	a.cfg.Logger.Log(Debug, "stream round done: tool_call deltas=%d", deltas)
	// Capture provider Usage from the stream result.
	// The usage chunk (stream_options.include_usage) is attached to
	// the stream result via End() or UpdateResult().
	a.captureStreamResult(stream)
	a.recordGenDuration()
	return true, a.completeStreamTurn(ctx), nil
}

// captureStreamResult records provider Usage and StopReason from a finished
// stream's result (the usage chunk arrives via stream_options.include_usage
// and is attached through End() or UpdateResult()).
func (a *Agent) captureStreamResult(stream *provider.AssistantMessageEventStream) {
	result := stream.Result()
	if result == nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if result.Usage != nil && !a.turnStatsEmitted {
		a.providerUsage = result.Usage
	}
	if result.Usage != nil {
		a.recordContextUsageLocked(result.Usage)
	}
	if result.StopReason != "" {
		a.lastStopReason = result.StopReason
	}
}

func (a *Agent) handleStreamError(_ context.Context, stream *provider.AssistantMessageEventStream, event provider.AssistantMessageEvent) (bool, bool, error) {
	return true, false, a.resolveStreamError(stream, event.Error)
}

// tryAutoHealToolCalls parses the accumulated assistant text for XML tool
// calls when AutoHealToolCalls is enabled and no native tool calls were
// buffered.  Discovered calls are run through the ToolLoopController and
// either buffered for execution or recorded as no-ops with a nudge message.
// It returns true when at least one call was discovered.
func (a *Agent) tryAutoHealToolCalls() bool {
	if !a.cfg.AutoHealToolCalls || len(a.bufferedToolCalls) > 0 {
		return false
	}

	content := a.contentBuf.String()
	thinking := a.thinkingBuf.String()
	combined := content
	if thinking != "" {
		if content != "" {
			combined += "\n"
		}
		combined += thinking
	}
	if !hasToolSignal(combined) {
		return false
	}

	a.emitEvent(OutputEvent{
		Type: EventProgress,
		Text: "Decoding tool calls...",
	})

	calls := parseToolCallsFromText(combined, 0, true)
	if len(calls) == 0 {
		return false
	}

	strippedContent := stripToolMarkup(content, true)
	a.contentBuf.Reset()
	a.contentBuf.WriteString(strippedContent)

	strippedThinking := stripToolMarkup(thinking, true)
	a.thinkingBuf.Reset()
	a.thinkingBuf.WriteString(strippedThinking)
	a.thinkingDisplayBuf.Reset()

	controller := NewToolLoopController(a.reg.Schemas(), a.reg.LoopHints(), true)
	for _, pc := range calls {
		decision := controller.PrepareCall(pc.name, pc.arguments, pc.id)
		switch decision.Action {
		case ActionExecute:
			a.bufferedToolCallCount++
			a.emitEvent(OutputEvent{
				Type:       EventToolCall,
				State:      StateToolCall,
				ToolName:   decision.ToolName,
				ToolInput:  decision.Arguments,
				ToolCallID: decision.ToolCallID,
			})
			a.bufferedToolCalls = append(a.bufferedToolCalls, provider.ContentBlock{
				Type:          provider.ContentBlockToolCall,
				ToolCallID:    decision.ToolCallID,
				ToolName:      decision.ToolName,
				ToolArguments: decision.Arguments,
			})
		case ActionDuplicate, ActionDisabled, ActionRenderHTMLRepeat:
			controller.RecordNoop(decision)
		}
	}
	return len(a.bufferedToolCalls) > 0 || controller.ForceFinalAnswer()
}

// completeStreamTurn finalizes the assistant buffer, executes buffered tool
// calls, and reports whether the agent loop should stream another round
// (true = a real tool executed and the model should be queried again). If a
// tool result requested that the batch stop after this result, the turn ends
// even if the model issued additional tool calls.
//
// When tool calls are present, finalizeStreamTurn is NOT called — the full
// assistant message (content + tool_calls) is assembled in
// executeBufferedToolCalls. Calling finalizeStreamTurn first would append a
// partial assistant message (content only), followed by a second full message
// from appendAssistantToolCallMessage, producing duplicate assistant messages
// that break prompt caching and corrupt the conversation structure.
//
// EventEnd semantics: EventEnd signals the end of the whole conversation
// turn, NOT the end of a single stream round. It is therefore emitted ONLY
// when the turn is actually finishing — either here (when no further round
// will run) or later via finalizeStreamTurn (once the model produces a final
// answer without tool calls). Emitting EventEnd after every tool batch made
// UI consumers (e.g. the status spinner) tear down turn state mid-turn, which
// silently dropped the spinner after the first tool call.
func (a *Agent) completeStreamTurn(ctx context.Context) bool {
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
		// only when a real tool executed and the batch was not asked to stop.
		// EventEnd is emitted exclusively on the finishing path so mid-turn UI
		// consumers never observe a premature turn end (which previously dropped
		// the status spinner after the first tool call).
		turnContinues := hadRealExecution && !a.stopBatchAfterThis
		if a.stopBatchAfterThis {
			a.stopBatchAfterThis = false
		}
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
const maxAutoContinuePerTurn = 3

// shouldAutoContinue reports whether a finish_reason=stop that ended the round
// looks premature: the turn did real tool work and the streamed answer is
// clearly truncated mid-task, and the auto-continue budget is not exhausted.
func (a *Agent) shouldAutoContinue() bool {
	if a.autoContinueCount >= maxAutoContinuePerTurn {
		return false
	}
	if !a.turnHadToolExecution {
		return false // no tool work → a plain (possibly short) answer is legitimate
	}
	a.mu.Lock()
	content := a.contentBuf.String()
	a.mu.Unlock()
	return looksTruncated(content)
}

// looksTruncated reports whether streamed answer text ends mid-task. Signals,
// in priority order: a trailing continuation marker (: , ; - ( …), a trailing
// intent phrase ("let me", "I'll", "I will", ...), or no terminal punctuation
// at all (a real summary ends with . ! ? or a markdown structure).
func looksTruncated(content string) bool {
	s := strings.TrimRight(content, " \t\r\n")
	if s == "" {
		return false // empty is handled by the empty-response / silent-stop guards
	}
	last := s[len(s)-1]
	// Terminal punctuation or closing markdown = complete.
	if strings.ContainsRune(".!?)`\"']}|>*_", rune(last)) {
		return false
	}
	// Explicit continuation markers.
	if strings.ContainsRune(":;,(-–—…", rune(last)) {
		return true
	}
	// Trailing intent phrase (last 40 chars, case-insensitive).
	tail := s
	if len(tail) > 40 {
		tail = tail[len(tail)-40:]
	}
	tail = strings.ToLower(tail)
	for _, phrase := range []string{"let me", "i'll", "i will", "i'm going to", "now i", "next i", "i need to", "let's"} {
		if strings.Contains(tail, phrase) {
			return true
		}
	}
	// No terminal punctuation and no closing structure → likely truncated.
	return true
}

// finishStreamTurn handles a stream that ended without an explicit EventDone.
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
			a.handleContextError(err)
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
func (a *Agent) resolveStreamError(stream *provider.AssistantMessageEventStream, eventErr error) error {
	// Detect context overflow BEFORE finalizing the turn so the
	// duplicate-EventEnd bug is avoided.  Check both eventErr and
	// stream.Err() since the error may be in either location.
	err := eventErr
	if err == nil {
		err = stream.Err()
	}
	if err != nil && isContextLengthError(err) {
		a.handleContextError(err)
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

func (a *Agent) handleTextDelta(event provider.AssistantMessageEvent) {
	a.resetThinkingStall()
	a.cfg.Logger.Log(Trace, "[delta] content: %s", event.Delta)
	a.mu.Lock()
	a.turnSawContent = true
	a.mu.Unlock()
	a.contentBuf.WriteString(event.Delta)
	a.checkStreamLoop(a.contentBuf.String())
	a.emitEvent(OutputEvent{Type: EventContent, State: StateContent, Role: Assistant, Text: event.Delta, IsDelta: true})
}

func (a *Agent) handleThinkingDelta(event provider.AssistantMessageEvent) {
	a.cfg.Logger.Log(Trace, "[delta] thinking: %s", event.Delta)
	a.mu.Lock()
	a.turnSawThinking = true
	a.mu.Unlock()
	a.thinkingBuf.WriteString(event.Delta)
	a.checkStreamLoop(a.thinkingBuf.String())

	// Track extended thinking without progress. This watchdog is independent
	// from the stream loop detector and toggled separately
	// (/config:temp:thinking_stall_detection:off or
	// execution.disable_thinking_stall_detection); checked per delta so a
	// mid-stream toggle takes effect immediately.
	if stallDisabled := a.cfg.ThinkingStallDisabled != nil && a.cfg.ThinkingStallDisabled(); !stallDisabled {
		warnAfter := a.cfg.ThinkingStallWarn
		if warnAfter <= 0 {
			warnAfter = defaultThinkingStallWarn
		}
		stopAfter := a.cfg.ThinkingStallStop
		if stopAfter <= 0 {
			stopAfter = defaultThinkingStallStop
		}
		if a.thinkingStallStart.IsZero() {
			a.thinkingStallStart = time.Now()
		}
		elapsed := time.Since(a.thinkingStallStart)
		if elapsed > stopAfter {
			a.cfg.Logger.Log(Warn, "Stopping stream: thinking stalled for %v without progress", elapsed)
			a.thinkingStalled = true
			a.thinkingStallElapsed = elapsed
			return
		}
		if elapsed > warnAfter && !a.thinkingStallWarned {
			a.thinkingStallWarned = true
			a.emitEvent(OutputEvent{
				Type: EventProgress,
				Text: "The agent has been thinking for over " + warnAfter.Round(time.Second).String() + " without producing output.",
			})
		}
	}

	// Strip tool-call XML from the visible thinking stream. Local
	// models sometimes emit <tool_call> or <function=> markup inside
	// reasoning_content; without this, raw XML is rendered in the thinking
	// block. The raw thinking buffer is still accumulated for auto-heal.
	a.thinkingDisplayBuf.WriteString(event.Delta)
	clean := stripToolMarkup(a.thinkingDisplayBuf.String(), true)
	if clean != "" && !containsToolXMLTag(clean) {
		a.emitEvent(OutputEvent{Type: EventContent, State: StateThinking, Role: Assistant, Text: clean, IsDelta: true})
		a.thinkingDisplayBuf.Reset()
	}
}

// handleToolCallPartial processes an incremental tool call event during
// streaming (EventToolCallStart, or EventToolCallDelta when the provider
// ships a full Partial snapshot such as OpenAI). It accumulates partial
// arguments and emits EventToolCall updates to observers so the TUI can
// display live progress as the model constructs the tool call.
//
// contentIndex correlates the call across later nil-Partial deltas (Anthropic
// ships input_json_delta with only Delta + ContentIndex); it is recorded so
// handleToolCallDeltaByIndex can append to the right call.
func (a *Agent) handleToolCallPartial(tc provider.ContentBlock, contentIndex int) {
	id := tc.ToolCallID
	if id == "" {
		id = tc.ToolName // fallback: some providers omit the ID on start
	}

	a.mu.Lock()
	ptc, exists := a.streamingToolCalls[id]
	if !exists {
		ptc = &partialToolCall{
			toolName:     tc.ToolName,
			toolCallID:   tc.ToolCallID,
			contentIndex: contentIndex,
		}
		if a.streamingToolCalls == nil {
			a.streamingToolCalls = make(map[string]*partialToolCall)
		}
		a.streamingToolCalls[id] = ptc
		a.indexStreamingToolCall(contentIndex, ptc)
	}
	if tc.ToolName != "" {
		ptc.toolName = tc.ToolName
	}
	if tc.ToolCallID != "" {
		ptc.toolCallID = tc.ToolCallID
	}
	if tc.ToolArguments != "" {
		ptc.argsBuf.WriteString(tc.ToolArguments)
	}
	accumulated := ptc.argsBuf.String()
	emitID := ptc.toolCallID
	emitName := ptc.toolName
	a.mu.Unlock()

	// Do not emit until the tool name is known: OpenAI-style streams ship the
	// call id/index in the first chunk and the name only in a later one, and a
	// nameless delta made the TUI create a blank-header tool widget that never
	// updated (bugs.md "Empty tool TUI"). Args accumulate here and are emitted
	// cumulative, so the first named delta carries the full prefix.
	if emitName == "" {
		return
	}

	// Emit partial EventToolCall to observers (TUI will show ◉ pending icon).
	a.emitEvent(OutputEvent{
		Type:       EventToolCall,
		State:      StateToolCall,
		Role:       Assistant,
		ToolName:   emitName,
		ToolInput:  accumulated,
		ToolCallID: emitID,
		IsDelta:    true,
	})
}

// handleToolCallDeltaByIndex appends an incremental JSON fragment to the
// streaming tool call identified by contentIndex and re-emits a partial
// EventToolCall. This is the Anthropic path: input_json_delta events carry
// only Delta + ContentIndex (no Partial snapshot), so without this the args
// would never stream to the TUI until the whole call completed.
func (a *Agent) handleToolCallDeltaByIndex(contentIndex int, delta string) {
	a.mu.Lock()
	ptc := a.streamingToolCallsByIndex[contentIndex]
	if ptc == nil {
		a.mu.Unlock()
		return
	}
	ptc.argsBuf.WriteString(delta)
	accumulated := ptc.argsBuf.String()
	emitID := ptc.toolCallID
	emitName := ptc.toolName
	a.mu.Unlock()

	// Same nameless guard as handleToolCallPartial (bugs.md "Empty tool TUI").
	if emitName == "" {
		return
	}

	a.emitEvent(OutputEvent{
		Type:       EventToolCall,
		State:      StateToolCall,
		Role:       Assistant,
		ToolName:   emitName,
		ToolInput:  accumulated,
		ToolCallID: emitID,
		IsDelta:    true,
	})
}

// indexStreamingToolCall records a partial call under its content-block index
// so nil-Partial deltas (Anthropic) can be correlated. Caller must hold a.mu.
func (a *Agent) indexStreamingToolCall(contentIndex int, ptc *partialToolCall) {
	if a.streamingToolCallsByIndex == nil {
		a.streamingToolCallsByIndex = make(map[int]*partialToolCall)
	}
	a.streamingToolCallsByIndex[contentIndex] = ptc
}

// containsToolXMLTag reports whether text still contains any raw tool-call XML
// tag (open or close). It is used while streaming thinking text so that
// multi-line tool-call markup that spans multiple deltas is suppressed until
// the whole block is closed and stripped.
func containsToolXMLTag(text string) bool {
	for _, tag := range []string{
		"<tool_call>", "</tool_call>",
		"<function=", "</function>",
		"<parameter=", "</parameter>",
	} {
		if strings.Contains(text, tag) {
			return true
		}
	}
	return false
}

// resetThinkingStall clears the thinking-stall tracking whenever the model
// produces content or a tool call, indicating forward progress.
func (a *Agent) resetThinkingStall() {
	a.thinkingStallStart = time.Time{}
	a.thinkingStallWarned = false
}

// resetStreamRoundState clears per-round buffers and flags before a re-stream
// or retry. This prevents a failed or truncated assistant response from
// leaking partial tokens or buffered tool calls into the next attempt.
func (a *Agent) resetStreamRoundState() {
	a.contentBuf.Reset()
	a.thinkingBuf.Reset()
	a.thinkingDisplayBuf.Reset()
	a.bufferedToolCalls = nil
	a.bufferedToolCallCount = 0
	a.streamLoopDetected = false
	a.streamLoopStrikeThisRound = false
	a.thinkingStalled = false
	a.thinkingStallElapsed = 0
	a.resetThinkingStall()
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
	if period, repeats, ok := streamLoopScan(clean, a.streamLoopMaxRepeats()); ok {
		a.streamLoopDetected = true
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

const (
	// defaultStreamLoopMaxStrikes is the number of stream-loop detections
	// after which the turn is stopped. Earlier detections are soft: the
	// looped round is abandoned, the model is warned with an ephemeral
	// hint, and the turn re-streams (execution.stream_loop_max_strikes).
	defaultStreamLoopMaxStrikes = 3
	// defaultStreamLoopResetAfter is the number of clean messages/tool
	// calls (no loop detected) after which the strike counter resets to
	// zero (execution.stream_loop_reset_after).
	defaultStreamLoopResetAfter = 10
)

// effectiveStreamLoopMaxStrikes resolves the strike limit, defaulting to 3.
func (a *Agent) effectiveStreamLoopMaxStrikes() int {
	if a.cfg.StreamLoopMaxStrikes > 0 {
		return a.cfg.StreamLoopMaxStrikes
	}
	return defaultStreamLoopMaxStrikes
}

// effectiveStreamLoopResetAfter resolves the clean-activity count that
// resets the strike counter, defaulting to 10.
func (a *Agent) effectiveStreamLoopResetAfter() int {
	if a.cfg.StreamLoopResetAfter > 0 {
		return a.cfg.StreamLoopResetAfter
	}
	return defaultStreamLoopResetAfter
}

// registerStreamLoopStrike records a stream-loop detection: the strike count
// increments and the clean-activity counter restarts. Returns the strike
// number (1-based).
func (a *Agent) registerStreamLoopStrike() int {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.streamLoopStrikes++
	a.streamLoopCleanCount = 0
	a.streamLoopStrikeThisRound = true
	return a.streamLoopStrikes
}

// noteStreamLoopCleanActivity records n clean messages/tool calls (no loop
// detected) and resets the strike counter once the configured clean streak
// (execution.stream_loop_reset_after, default 10) is reached.
func (a *Agent) noteStreamLoopCleanActivity(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.streamLoopStrikes == 0 || n <= 0 {
		return
	}
	a.streamLoopCleanCount += n
	if a.streamLoopCleanCount >= a.effectiveStreamLoopResetAfter() {
		a.cfg.Logger.Log(Info, "Stream-loop strike counter reset after %d clean messages/tool calls", a.streamLoopCleanCount)
		a.streamLoopStrikes = 0
		a.streamLoopCleanCount = 0
	}
}

// noteCleanStreamRound counts a stream round that completed without a loop
// strike as one clean message toward resetting the strike counter.
func (a *Agent) noteCleanStreamRound() {
	a.mu.Lock()
	strike := a.streamLoopStrikeThisRound
	a.mu.Unlock()
	if strike {
		return
	}
	a.noteStreamLoopCleanActivity(1)
}

// handleStreamLoopStrike applies the graduated stream-loop response: the
// detections below the strike limit are soft (the looped round is abandoned,
// the model is warned with an ephemeral hint, and the turn re-streams); the
// detection at the limit stops the turn with an error. Reports whether the
// turn continues (soft strike) or the terminal error (hard strike).
func (a *Agent) handleStreamLoopStrike(ctx context.Context) (toolCallsEncountered bool, err error) {
	strike := a.registerStreamLoopStrike()
	maxStrikes := a.effectiveStreamLoopMaxStrikes()
	if strike >= maxStrikes {
		a.cfg.Logger.Log(Warn, "Stream loop strike %d/%d: the model kept repeating; stopping the turn", strike, maxStrikes)
		return false, fmt.Errorf("stream loop detected: the assistant kept repeating the same text after %d warnings; turn stopped to prevent runaway context usage", strike-1)
	}
	a.cfg.Logger.Log(Warn, "Stream loop strike %d/%d: abandoning the looped round and warning the model", strike, maxStrikes)
	return a.recoverFromStreamLoop(ctx, strike, maxStrikes), nil
}

// recoverFromStreamLoop handles a soft stream-loop strike: the looped round
// is abandoned — its repetition-laden partial text is NOT committed to
// history — the user is shown a warning, and an ephemeral hint tells the
// model to continue without repeating, so the next round re-streams. When
// the round already buffered complete tool calls, they run through the
// normal execution path so the model keeps their results. Always reports
// that the turn continues (a soft strike never ends the turn directly; the
// tool-call path may still end it via the normal budget rules).
func (a *Agent) recoverFromStreamLoop(ctx context.Context, strike, maxStrikes int) bool {
	a.emitEvent(OutputEvent{
		Type: EventContent,
		Role: System,
		Text: fmt.Sprintf("Stream loop detected (warning %d of %d) — the reply was cut off; the model was told to continue without repeating.", strike, maxStrikes),
		// stream_retry retracts the orphaned in-progress assistant bubble:
		// the looped partial text is discarded, not finalized, so without a
		// retraction it would linger next to the re-streamed answer.
		Metadata: map[string]string{"category": "system-notification", "stream_retry": "true"},
	})
	a.InjectEphemeralSystemMessage(
		"[goa-system] Internal control note (never show or mention to the user): your previous output started " +
			"repeating the same block of text over and over and was cut off. Do not repeat yourself. Continue the " +
			"task now: move forward, keep the answer concise, and do not restate earlier text.")
	if len(a.bufferedToolCalls) > 0 {
		// Complete tool calls arrived before the loop started: execute them
		// through the normal path so the model keeps their results. The
		// ephemeral warning rides along in history for the next round.
		return a.completeStreamTurn(ctx)
	}
	return true
}

// streamLoopScan is the detection core of checkStreamLoop: it reports whether
// the normalized buffer ends in a repeated unit, and if so returns the unit
// size and repeat count. Kept separate from the Agent method so the exact
// production scan can be exercised directly by tests.
//
// Detection policy (count-based rewrite after field failures in BOTH
// directions — a false positive on exploratory Option A/B/C analysis and a
// false negative on a ~90-copy paraphrase loop; see bugs.md 2026-08-01):
//
//   - Detector A (exact chain): the trailing unit of length P
//     (P ≥ streamLoopExactMinPeriod) is a loop when it repeats BYTE-EXACT
//     ≥ maxRepeats times (≥ 2 for P ≥ streamLoopLongPeriod), allowing ≤
//     streamLoopMaxGap interlude bytes between copies. No fuzzy matching, no
//     progression analysis: exploratory paragraphs never repeat 60+ exact
//     bytes, and connector noise ("the the the …") lives below the floor.
//   - Detector B (paraphrase coverage): a loop whose copies drift in wording
//     has no exact unit, but its words are almost all inside a handful of
//     repeated shingles. Fire when ≥ streamLoopMinHotShingles distinct
//     shingles are "hot" (≥ streamLoopShingleHot occurrences) AND they cover
//     ≥ streamLoopMinCoverage of the tail words. A 3–4 paragraph Option
//     A/B/C analysis has almost no hot shingles; repeating one TERM has too
//     few hot shingles; enumerated lists have unique shingles.
//   - Only the trailing streamLoopTailWindow bytes are scanned, bounding the
//     per-delta cost.
func streamLoopScan(clean string, maxRepeats int) (period, repeats int, ok bool) {
	if maxRepeats < 2 {
		maxRepeats = 2
	}
	tail := clean
	if len(tail) > streamLoopTailWindow {
		tail = tail[len(tail)-streamLoopTailWindow:]
	}
	if uniqueWordCount(tail) < 3 {
		// A tail of one or two unique words ("the the the …", "ok ok …") is
		// connector noise, not repeated content; the loop detectors need at
		// least three distinct words to have an opinion.
		return 0, 0, false
	}
	if period, repeats, ok := streamExactChain(tail, maxRepeats); ok {
		return period, repeats, true
	}
	return streamParaphraseLoop(tail, maxRepeats)
}

// uniqueWordCount counts distinct space-separated words in s, stopping early
// at 3 (only the <3 case matters to the caller).
func uniqueWordCount(s string) int {
	seen := make(map[string]struct{}, 8)
	for _, w := range strings.Fields(s) {
		seen[w] = struct{}{}
		if len(seen) >= 3 {
			break
		}
	}
	return len(seen)
}

const (
	// streamLoopExactMinPeriod is the smallest repeated unit Detector A
	// considers: shorter exact repeats are punctuation/connector noise. All
	// field false positives were NON-exact, so exact-only matching is safe
	// at this floor; a genuine micro-loop with a shorter unit also repeats
	// at a multiple of the unit, which qualifies.
	streamLoopExactMinPeriod = 60
	// streamLoopLongPeriod is the unit size from which two byte-exact copies
	// already count as a loop: nobody legitimately repeats a kilobyte twice.
	streamLoopLongPeriod = 1024
	// streamLoopMaxGap bounds the interlude allowed between chained copies
	// so "repeat with a one-line interjection" loops still trip.
	streamLoopMaxGap = 64
	// streamLoopTailWindow bounds the scanned tail (and per-delta cost).
	streamLoopTailWindow = 4096
	// streamLoopSmallPeriod is the smallest period scanned at all; below it
	// only connector noise lives ("the the the …").
	streamLoopSmallPeriod = 8
	// streamLoopShingleWords is the shingle size for Detector B.
	streamLoopShingleWords = 3
	// streamLoopShingleHot is the base occurrence count making a shingle
	// "hot" (raised to the configured maxRepeats when that is higher).
	streamLoopShingleHot = 4
	// streamLoopMinHotShingles is the number of distinct hot shingles a
	// paraphrase loop must have: a couple of repeated terms is topical
	// emphasis, not a loop.
	streamLoopMinHotShingles = 4
	// streamLoopMinWords is the tail word floor for Detector B.
	streamLoopMinWords = 80
	// streamLoopMinCoverage is the fraction of tail words that must sit
	// inside hot shingles: paraphrase loops are dominated by their template,
	// while topical repetition keeps repeated fragments a small minority.
	streamLoopMinCoverage = 0.4
)

// streamExactChain implements Detector A: for each candidate period, chain
// byte-exact copies of the trailing unit backward through the tail.
//
// Required copy count (certainty rises with unit size and count):
//   - P ≥ streamLoopLongPeriod: 2 copies (nobody repeats a kilobyte twice)
//   - streamLoopExactMinPeriod ≤ P < long: max(maxRepeats, 3) — a pair of
//     sub-kilobyte quotes is evidence, not a loop, at any knob setting
//   - streamLoopSmallPeriod ≤ P < exactMin: max(maxRepeats, 8) — micro-loops
//     need overwhelming count
func streamExactChain(tail string, maxRepeats int) (period, repeats int, ok bool) {
	for p := streamLoopSmallPeriod; p <= len(tail)/2; p++ {
		required, gap, skip := chainRules(tail, p, maxRepeats)
		if skip {
			continue
		}
		if n := chainCopies(tail, p, gap); n >= required {
			return p, n, true
		}
	}
	return 0, 0, false
}

// chainRules returns the required copy count and interlude gap for a
// candidate period, and whether the period must be skipped entirely.
func chainRules(tail string, p, maxRepeats int) (required, gap int, skip bool) {
	switch {
	case p >= streamLoopLongPeriod:
		return 2, streamLoopMaxGap, false
	case p >= streamLoopExactMinPeriod:
		if maxRepeats < 3 {
			maxRepeats = 3
		}
		return maxRepeats, streamLoopMaxGap, false
	default:
		// Micro-units must be real word content: word fragments ("reopen pa"
		// riding a repeated term) are not loops.
		if len(strings.Fields(tail[len(tail)-p:])) < 3 {
			return 0, 0, true
		}
		if maxRepeats < 8 {
			maxRepeats = 8
		}
		// Tight chaining only: scattered occurrences are topical, not loops.
		return maxRepeats, p / 2, false
	}
}

// chainCopies counts how many byte-exact copies of the trailing p-byte unit
// chain backward through the tail, allowing up to gap interlude bytes
// between copies.
func chainCopies(tail string, p, gap int) int {
	unit := tail[len(tail)-p:]
	n, pos := 1, len(tail)-p
	for {
		lo := pos - p - gap
		if lo < 0 {
			lo = 0
		}
		idx := strings.LastIndex(tail[lo:pos], unit)
		if idx < 0 {
			return n
		}
		n++
		pos = lo + idx
	}
}

// streamParaphraseLoop implements Detector B: count word shingles in the
// tail and fire when enough distinct shingles are hot and they cover most of
// the tail words. The hot threshold tracks the configured repeat tolerance
// (never below streamLoopShingleHot), so a high maxRepeats knob also raises
// the paraphrase bar.
func streamParaphraseLoop(tail string, maxRepeats int) (period, repeats int, ok bool) {
	words := strings.Fields(tail)
	if len(words) < streamLoopMinWords {
		return 0, 0, false
	}
	hot := max(streamLoopShingleHot, maxRepeats)
	n := streamLoopShingleWords
	counts := shingleCounts(words, n)
	if hotShingleKeys(counts, hot) < streamLoopMinHotShingles {
		return 0, 0, false
	}
	coveredN := shingleCoveredWords(words, counts, hot, n)
	if float64(coveredN)/float64(len(words)) < streamLoopMinCoverage {
		return 0, 0, false
	}
	return n, coveredN / n, true
}

// shingleCounts counts overlapping n-word shingles over words.
func shingleCounts(words []string, n int) map[string]int {
	counts := make(map[string]int, len(words))
	for i := 0; i+n <= len(words); i++ {
		counts[strings.Join(words[i:i+n], " ")]++
	}
	return counts
}

// hotShingleKeys counts distinct shingles occurring at least hot times.
func hotShingleKeys(counts map[string]int, hot int) int {
	hotKeys := 0
	for _, c := range counts {
		if c >= hot {
			hotKeys++
		}
	}
	return hotKeys
}

// shingleCoveredWords counts word positions covered by any hot shingle.
func shingleCoveredWords(words []string, counts map[string]int, hot, n int) int {
	covered := make([]bool, len(words))
	coveredN := 0
	for i := 0; i+n <= len(words); i++ {
		if counts[strings.Join(words[i:i+n], " ")] < hot {
			continue
		}
		for j := i; j < i+n; j++ {
			if !covered[j] {
				covered[j] = true
				coveredN++
			}
		}
	}
	return coveredN
}

// streamLoopNormalize strips everything except letters, digits, and spaces,
// folds case, then collapses runs of spaces. This prevents punctuation,
// symbols, box-drawing characters, and casing drift from causing false
// positive (or false negative) loop detections.
func streamLoopNormalize(text string) string {
	var b strings.Builder
	b.Grow(len(text) / 2)
	prevSpace := false
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(unicode.ToLower(r))
			prevSpace = false
		} else if unicode.IsSpace(r) && !prevSpace {
			b.WriteRune(' ')
			prevSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}
func (a *Agent) prepareTurn(ctx context.Context) (provider.Model, provider.StreamOptions, provider.Context) {
	a.mu.Lock()
	a.turnToolCalls = make(map[string]int)
	a.turnToolCallCount = 0
	a.turnHadToolExecution = false
	a.turnSawContent = false
	a.turnSawThinking = false
	a.contentBuf.Reset()
	a.thinkingBuf.Reset()
	a.thinkingDisplayBuf.Reset()
	a.turnStatsEmitted = false
	a.turnStartHistoryLen = len(a.history)
	a.bufferedToolCalls = nil
	a.bufferedToolCallCount = 0
	a.budgetToolCalls = make(map[string]string)
	a.stopBatchAfterThis = false
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
	a.mu.Unlock()

	if err := a.maybeCompress(ctx); err != nil {
		a.cfg.Logger.Log(Error, "proactive compression failed: %v", err)
	}
	a.enforceContextCeiling()

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
	if !shouldRetryStreamError(ctx, streamErr) {
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
	// (bugs.md Issue 4 — streaming repeats that shift on scroll).
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

// retryStream attempts to reconnect up to two times after a stream error.
// Returns whether any retry succeeded and whether a tool call was encountered.
// On context cancellation the function returns promptly instead of sleeping
// through the full backoff window.
func (a *Agent) retryStream(ctx context.Context, originalErr error, model provider.Model, opts provider.StreamOptions) (toolCallEncountered bool, retried bool) {
	var streamErr error
	for retry := 0; retry < opts.MaxRetries; retry++ {
		a.cfg.Logger.Log(Info, "retry attempt %d/%d after stream error", retry+1, opts.MaxRetries)
		a.emitEvent(OutputEvent{Type: EventProgress, Text: fmt.Sprintf("Reconnecting (attempt %d/%d)...", retry+1, opts.MaxRetries)})

		// Sleep with context awareness so Ctrl+C isn't ignored during backoff.
		// retryBackoff honors a server-supplied Retry-After for rate limits and
		// otherwise uses bounded exponential backoff with jitter.
		select {
		case <-time.After(retryBackoff(originalErr, retry)):
		case <-ctx.Done():
			return false, false
		}

		pCtx := a.buildProviderContext(ctx)
		stream, err := provider.Stream(model, pCtx, opts)
		if err != nil {
			a.cfg.Logger.Log(Warn, "retry stream failed: %v", err)
			continue
		}
		toolCallEncountered, streamErr = a.consumeStream(ctx, stream, opts)
		if streamErr == nil {
			a.emitEvent(OutputEvent{Type: EventProgress, Text: ""})
			// Durable confirmation so the retry lifecycle is visible in chat
			// history — failure bubble (episode start) + spinner attempts
			// (live) + this restored bubble (success) — not only a transient
			// spinner line (bugs.md Issue 17).
			a.emitEvent(OutputEvent{
				Type:     EventContent,
				Role:     System,
				Text:     fmt.Sprintf("Connection restored (attempt %d/%d) — resuming.", retry+1, opts.MaxRetries),
				Metadata: map[string]string{"category": "system-notification"},
			})
			return toolCallEncountered, true
		}
		// Clean up after the failed retry so the next attempt (or error path)
		// does not inherit partial tokens, buffered tool calls, or a spurious
		// assistant message.
		a.mu.Lock()
		a.resetStreamRoundState()
		a.mu.Unlock()
		a.undoLastAssistantMessage()
		a.cfg.Logger.Log(Warn, "retry attempt %d also failed: %v", retry+1, streamErr)
	}
	return false, false
}

func (a *Agent) buildProviderContext(ctx context.Context) provider.Context {
	msgs := a.buildProviderHistory()
	sp := a.buildSystemPrompt()
	msgs = mergeGoalProgress(msgs, a.cfg.GoalStateProvider)

	return provider.Context{
		Context:      ctx,
		SystemPrompt: sp,
		Messages:     msgs,
		Tools:        migrateSchemas(a.reg.Schemas()),
	}
}

func (a *Agent) buildProviderHistory() []provider.Message {
	a.mu.Lock()
	defer a.mu.Unlock()
	msgs := make([]provider.Message, 0, len(a.history))
	for i, m := range a.history {
		// Skip only the initial system prompt message; the provider context
		// carries it separately via SystemPrompt. Later system messages (for
		// example runtime tool-change notifications) must still be sent.
		if i == 0 && a.cfg.SystemPrompt != "" && m.Role == System {
			continue
		}
		msgs = append(msgs, migrateMessage(m))
	}
	return msgs
}

func (a *Agent) buildSystemPrompt() string {
	// The system prompt is the provider-cached prefix: it must stay
	// byte-identical across the whole session, including goal create/destroy/
	// status-flips. Goal text therefore does NOT belong here — it is injected
	// as volatile slot messages by mergeGoalProgress instead (bugs.md
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
// Trade-offs (decided 2026-07-21, bugs.md): reminders persist, so a
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
		msg := Message{Type: Content, Role: User, Content: "[goal]\n" + reminder}
		a.history = append(a.history, msg)
		a.emitMessage(msg)
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