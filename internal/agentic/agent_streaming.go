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
	}
}

// runStreamRound performs one LLM stream round, handling tool calls,
// progress checks, and stream failures. It returns done=true when the turn
// should end after this round (no further tool calls to process).
func (a *Agent) runStreamRound(ctx context.Context, round int, model provider.Model, opts provider.StreamOptions, initCtx provider.Context, maxStreams *int) (done bool, err error) {
	stream, err := a.startStreamRound(ctx, round, model, opts, initCtx)
	if err != nil {
		return false, err
	}

	toolCallEncountered, streamErr := a.consumeStream(ctx, stream, opts)
	if streamErr != nil {
		if handled, retErr := a.handleStreamFailure(ctx, streamErr, model, opts); handled {
			if retErr != nil {
				return false, retErr
			}
			// Retry succeeded and produced no further tool calls: turn is done.
			return true, nil
		}
		return false, nil
	}

	if !toolCallEncountered {
		return true, nil
	}

	// Check whether the tool-call round limit is reached and the model has stalled.
	// If so, run the recovery stream (which injects a hint and does a final LLM
	// call). The recovery stream is the last chance for this turn, so the turn
	// ends when it returns.
	if round >= *maxStreams-1 && a.hasStalled() {
		if err := a.runRecoveryStream(ctx, model, opts, *maxStreams); err != nil {
			return false, err
		}
		return true, nil
	}

	// Extend horizon if still making progress.
	if round >= *maxStreams-1 {
		next := *maxStreams + 50
		a.cfg.Logger.Log(Warn, "Extending stream horizon from %d to %d (model making progress)", *maxStreams, next)
		*maxStreams = next
	}
	return false, nil
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
	recovery := "[goa-system] The per-turn tool-call round limit was reached. Stop calling tools and complete the task using the information you have already gathered."
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

	// If no buffered calls at all, we can't judge progress.
	if len(a.bufferedToolCalls) == 0 {
		return true
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
	a.genStartTime = time.Time{} // reset per stream; recorded on first token

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
func (a *Agent) handleStreamEvent(ctx context.Context, stream *provider.AssistantMessageEventStream, event provider.AssistantMessageEvent) (done bool, toolCallsEncountered bool, err error) {
	if handler, ok := streamEventHandlers[event.Type]; ok {
		return handler(a, ctx, stream, event)
	}
	if a.streamLoopDetected {
		a.cfg.Logger.Log(Warn, "Stopping stream because a loop was detected inside the assistant response")
		return true, false, fmt.Errorf("stream loop detected: the assistant started repeating the same text; turn stopped to prevent runaway context usage")
	}
	return false, false, nil
}

// streamEventHandler processes a single stream event and reports whether the
// stream has reached a terminal state.
type streamEventHandler func(*Agent, context.Context, *provider.AssistantMessageEventStream, provider.AssistantMessageEvent) (done bool, toolCallsEncountered bool, err error)

var streamEventHandlers = map[provider.EventType]streamEventHandler{
	provider.EventTextDelta:       (*Agent).handleStreamTextDelta,
	provider.EventThinkingDelta:   (*Agent).handleStreamThinkingDelta,
	provider.EventToolCallEnd:     (*Agent).handleStreamToolCallEnd,
	provider.EventToolCallStart:   (*Agent).handleStreamToolCallStart,
	provider.EventToolCallDelta:   (*Agent).handleStreamToolCallDelta,
	provider.EventDone:            (*Agent).handleStreamDone,
	provider.EventError:           (*Agent).handleStreamError,
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
	if result := stream.Result(); result != nil && result.Usage != nil && !a.turnStatsEmitted {
		a.mu.Lock()
		a.providerUsage = result.Usage
		a.mu.Unlock()
	}
	a.recordGenDuration()
	return true, a.completeStreamTurn(ctx), nil
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

	// No tool calls: finalizeTurn appends the message and emits end events.
	a.finalizeStreamTurn()
	return false
}

// finishStreamTurn handles a stream that ended without an explicit EventDone.
func (a *Agent) finishStreamTurn(ctx context.Context, stream *provider.AssistantMessageEventStream) (bool, error) {
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
	if result := stream.Result(); result != nil && result.Usage != nil && !a.turnStatsEmitted {
		a.mu.Lock()
		a.providerUsage = result.Usage
		a.mu.Unlock()
	}
	a.recordGenDuration()

	// Empty-response guard: a clean stream end (2xx + [DONE]/EOF) that emitted
	// no stream events at all (no text/thinking/tool-call deltas — genStartTime
	// was never set) is not a legitimate answer when the model has done no tool
	// work this turn. Under provider load it indicates a truncated/failed
	// response, so it is routed through handleStreamFailure (bounded retry,
	// then a surfaced message) instead of ending the turn silently. It is
	// scoped to turns with no real tool execution: after a tool runs and its
	// result is sent back, an empty follow-up is a legitimate "done, nothing
	// more to say" turn end. A stream that emitted events but produced empty
	// text (e.g. loop-detector fixtures) sets genStartTime and is NOT treated
	// as empty here; thinking-only turns are handled by the silent-stop notice
	// in finalizeStreamTurn.
	if !a.cfg.AllowEmptyResponse && !a.turnHadToolExecution && a.genStartTime.IsZero() && len(a.bufferedToolCalls) == 0 {
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
	a.contentBuf.WriteString(event.Delta)
	a.checkStreamLoop(a.contentBuf.String())
	a.emitEvent(OutputEvent{Type: EventContent, State: StateContent, Role: Assistant, Text: event.Delta, IsDelta: true})
}

func (a *Agent) handleThinkingDelta(event provider.AssistantMessageEvent) {
	a.cfg.Logger.Log(Trace, "[delta] thinking: %s", event.Delta)
	a.thinkingBuf.WriteString(event.Delta)
	a.checkStreamLoop(a.thinkingBuf.String())

	// Track extended thinking without progress.
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
		a.streamLoopDetected = true
		return
	}
	if elapsed > warnAfter && !a.thinkingStallWarned {
		a.thinkingStallWarned = true
		a.emitEvent(OutputEvent{
			Type: EventProgress,
			Text: "The agent has been thinking for over " + warnAfter.Round(time.Second).String() + " without producing output.",
		})
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
	// Normalize: strip punctuation, symbols, box-drawing chars, collapse spaces
	clean := streamLoopNormalize(text)

	minWindow, maxWindow := streamLoopWindowRange(clean)
	if minWindow == 0 {
		return
	}

	for window := minWindow; window <= maxWindow; window++ {
		repeatsNeeded := streamLoopRepeatsNeeded(window)
		if streamHasRepeatedSuffix(clean, window, repeatsNeeded) {
			// Verify the repeated pattern is more than a single word.
			suffix := clean[len(clean)-window:]
			if !streamHasMultipleUniqueWords(suffix) {
				continue
			}
			a.streamLoopDetected = true
			a.cfg.Logger.Log(Warn, "Stream loop detected: %d-byte suffix repeated %d times", window, repeatsNeeded)
			return
		}
	}
}

// streamLoopNormalize strips everything except letters, digits, and spaces,
// then collapses runs of spaces. This prevents punctuation, symbols, and
// box-drawing characters from causing false positive loop detections.
func streamLoopNormalize(text string) string {
	var b strings.Builder
	b.Grow(len(text) / 2)
	prevSpace := false
	for _, r := range text {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			prevSpace = false
		} else if unicode.IsSpace(r) && !prevSpace {
			b.WriteRune(' ')
			prevSpace = true
		}
	}
	return strings.TrimSpace(b.String())
}

// streamHasMultipleUniqueWords reports whether s contains at least two
// *unique* words. This prevents single-word repetition like "the the the"
// from triggering a false positive loop detection.
func streamHasMultipleUniqueWords(s string) bool {
	words := strings.Fields(s)
	if len(words) < 2 {
		return false
	}
	seen := make(map[string]int, len(words))
	for _, w := range words {
		seen[w]++
	}
	return len(seen) >= 2
}

// streamLoopWindowRange returns the inclusive window-size range to scan for
// streaming repetition. It returns (0, 0) when the text is too short or too
// long to examine.
func streamLoopWindowRange(text string) (min, max int) {
	const (
		minWindow = 20
		maxWindow = 120
	)
	if len(text) < minWindow*2 {
		return 0, 0
	}
	max = len(text) / 2
	if max > maxWindow {
		max = maxWindow
	}
	if max < minWindow {
		return 0, 0
	}
	return minWindow, max
}

// streamLoopRepeatsNeeded returns how many consecutive occurrences of a
// window-sized suffix are required before it is considered a loop. Shorter
// windows need more repeats to avoid false positives from common phrases.
func streamLoopRepeatsNeeded(window int) int {
	if window >= 80 {
		return 2
	}
	return 3
}

// streamHasRepeatedSuffix reports whether text ends with the same window-sized
// substring repeated repeatsNeeded times consecutively.
func streamHasRepeatedSuffix(text string, window, repeatsNeeded int) bool {
	if len(text) < window*repeatsNeeded {
		return false
	}
	suffix := text[len(text)-window:]
	for r := 1; r < repeatsNeeded; r++ {
		block := text[len(text)-window*(r+1) : len(text)-window*r]
		if block != suffix {
			return false
		}
	}
	return true
}

// emitBudgetToolSkipped emits the TUI events (tool call + tool result) for a
// tool call that was rejected because the per-turn budget was exceeded, WITHOUT
// executing the tool. The result text instructs the model to answer from what
// it has already gathered.
//
// History is NOT mutated here. The call is buffered and the assistant message
// + budget result are appended once, together with all sibling calls, in
// executeBufferedToolCalls. Mutating history here would produce two assistant
// messages for a single turn and corrupt the tool_calls/tool_results pairing
// (breaks strict OpenAI-style providers such as DeepSeek).

func (a *Agent) prepareTurn(ctx context.Context) (provider.Model, provider.StreamOptions, provider.Context) {
	a.mu.Lock()
	a.turnToolCalls = make(map[string]int)
	a.turnToolCallCount = 0
	a.turnHadToolExecution = false
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
	a.streamLoopDetected = false
	a.overflowRecoveryAttempted = false
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
	if !shouldRetryStreamError(streamErr) {
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
	a.emitEvent(OutputEvent{
		Type:     EventContent,
		Role:     System,
		Text:     formatRetryMessage(streamErr),
		Metadata: map[string]string{"category": "system-notification"},
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
	for retry := 0; retry < 2; retry++ {
		a.cfg.Logger.Log(Info, "retry attempt %d after stream error", retry+1)
		a.emitEvent(OutputEvent{Type: EventProgress, Text: fmt.Sprintf("Reconnecting (attempt %d/2)...", retry+1)})

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
	sp := a.cfg.SystemPrompt
	if p := a.cfg.GoalStateProvider; p != nil {
		if reminder := p.ActiveGoalReminder(); reminder != "" {
			sp = reminder + "\n\n" + sp
		}
	}
	return sp
}

// mergeGoalProgress injects the dynamic per-turn goal progress as a dedicated
// system message placed immediately before the last user message, returning the
// (possibly grown) slice.
//
// It deliberately does NOT mutate the last user message's content. The previous
// implementation prepended the progress text to the last user message each turn;
// because that message joins the cached prefix on the next turn, and because it
// lost its prefix once it was no longer last, its bytes were turn-specific and
// busted the provider's prompt cache from that point on. A separate progress
// slot keeps every history message byte-stable across turns, so the cached
// prefix survives and only the volatile trailing progress is re-sent.
func mergeGoalProgress(msgs []provider.Message, p GoalStateProvider) []provider.Message {
	if p == nil {
		return msgs
	}
	progress := p.ActiveGoalProgress()
	if progress == "" {
		return msgs
	}
	progressMsg := provider.Message{
		Role: provider.RoleSystem,
		Content: []provider.ContentBlock{{
			Type: provider.ContentBlockText,
			Text: "[goal progress]\n" + progress,
		}},
	}
	// Insert just before the last user message so the conversation still ends
	// on a user turn (required by most providers).
	insertAt := len(msgs)
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == provider.RoleUser {
			insertAt = i
			break
		}
	}
	msgs = append(msgs, provider.Message{}) // grow by one
	copy(msgs[insertAt+1:], msgs[insertAt:])
	msgs[insertAt] = progressMsg
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
