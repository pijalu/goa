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

	toolCallEncountered, streamErr := a.consumeStream(ctx, stream)
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
	a.InjectSystemMessage(recovery)

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

		toolCallEncountered, streamErr := a.consumeStream(ctx, recoveryStream)
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

func (a *Agent) consumeStream(ctx context.Context, stream *provider.AssistantMessageEventStream) (bool, error) {
	a.genStartTime = time.Time{} // reset per stream; recorded on first token
	for event := range stream.Seq() {
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
	switch event.Type {
	case provider.EventTextDelta:
		a.markGenStart()
		a.handleTextDelta(event)
	case provider.EventThinkingDelta:
		a.markGenStart()
		a.handleThinkingDelta(event)
	case provider.EventToolCallEnd:
		if event.ToolCall != nil {
			a.markGenStart()
			a.resetThinkingStall()
			a.shouldBufferToolCall(*event.ToolCall)
		}
	case provider.EventDone:
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
	case provider.EventError:
		return true, false, a.resolveStreamError(stream, event.Error)
	}

	if a.streamLoopDetected {
		a.cfg.Logger.Log(Warn, "Stopping stream because a loop was detected inside the assistant response")
		return true, false, fmt.Errorf("stream loop detected: the assistant started repeating the same text; turn stopped to prevent runaway context usage")
	}
	return false, false, nil
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
// calls, and reports whether any tool calls were encountered. If a tool
// result requested that the batch stop after this result, the turn ends
// even if the model issued additional tool calls.
//
// When tool calls are present, finalizeStreamTurn is NOT called — the full
// assistant message (content + tool_calls) is assembled in
// executeBufferedToolCalls. Calling finalizeStreamTurn first would append a
// partial assistant message (content only), followed by a second full message
// from appendAssistantToolCallMessage, producing duplicate assistant messages
// that break prompt caching and corrupt the conversation structure.
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
		a.emitEvent(OutputEvent{Type: EventEnd})
		if a.stopBatchAfterThis {
			a.stopBatchAfterThis = false
			return false
		}
		return hadRealExecution
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
