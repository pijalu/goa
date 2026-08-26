// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

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
	// a long tool-work streak, truncating the reply mid-sentence; others stop
	// right after a thinking block or immediately after receiving tool
	// results without ever producing an answer. When the round is clearly
	// unfinished, auto-continue with a steer instead of ending the turn — the
	// user should not have to type "continue".
	if kind := a.classifyPrematureStop(); kind != prematureStopNone {
		a.autoContinueCount++
		a.cfg.Logger.Log(Warn, "premature stop detected (%s); auto-continuing turn (attempt %d/%d)", kind.describe(), a.autoContinueCount, maxAutoContinuePerTurn)
		a.InjectEphemeralSystemMessage(kind.steerNote())
		return true // treat like a continuing round; re-stream
	}

	// No tool calls: finalizeTurn appends the message and emits end events.
	a.finalizeStreamTurn(ctx)
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

// finalizeStreamTurn applies the reply:pre seam, appends the assistant buffer
// to history and emits EventEnd.
func (a *Agent) finalizeStreamTurn(ctx context.Context) {
	// Plugin seam (M1): reply:pre — the last chance to rewrite the finished
	// reply before the single append below (the ordering anchor for this
	// point; asserted in plugin_hooks_test.go).
	msg := a.applyReplyPreHook(ctx, a.synthesizeAssistantBuffer())
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
