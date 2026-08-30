// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package agentic provides a Go SDK for building AI agents that interact with
// LLMs and execute tools. The core abstraction is the Agent, which manages
// conversation state, tool execution, and event emission.
package agentic

import (
	"context"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// ReasoningEffort controls how much reasoning a model performs.
// Values are provider-specific (e.g., "low"/"medium"/"high" for OpenAI,
// "on"/"off" for Gemma).
type ReasoningEffort string

const (
	ReasoningEffortLow    ReasoningEffort = "low"
	ReasoningEffortMedium ReasoningEffort = "medium"
	ReasoningEffortHigh   ReasoningEffort = "high"
	ReasoningEffortXHigh  ReasoningEffort = "xhigh"
	ReasoningEffortOn     ReasoningEffort = "on"
	ReasoningEffortOff    ReasoningEffort = "off"
)

// Agent orchestrates conversations with an LLM provider, managing tool
// execution, conversation history, and event broadcasting to observers.
//
// Create an Agent using NewAgent with a Config that specifies the model,
// system prompt, tools, and optional logger.
//
// The Agent emits events via the Output channel and to registered observers.
// Use AddObserver to receive structured events for UI updates, logging, etc.
//
// Example:
//
//	agent := agentic.NewAgent(agentic.Config{
//	    Model:         myModel,
//	    StreamOptions: opts,
//	    SystemPrompt:  "You are a helpful assistant.",
//	    Tools:         []agentic.Tool{MyTool{}},
//	})
//	agent.Run(ctx, "Hello!")
type Agent struct {
	cfg     Config
	reg     ToolLookup
	history []Message
	// cacheContextID owns this agent's provider cache namespace. Generation is
	// advanced only when history is replaced or explicitly reset; append-only
	// turns retain the same opaque prompt-cache key.
	cacheContextID string
	// cacheGeneration advances whenever retained history changes IDENTITY —
	// wholesale replacement, summarization, or dropping oldest messages — so
	// the derived cache key rotates and can never alias a prefix the provider
	// no longer has (Hard Rule 7: only a byte-exact append may keep the key).
	// In-place payload rewrites that preserve the message SKELETON (tool-result
	// elision, micro-compaction, ephemeral strips) deliberately keep the
	// generation: providers with partial-prefix matching can still hit the
	// unchanged head, and the residual miss is exactly what the forensics
	// journal reports.
	cacheGeneration uint64
	// activeCacheKey is the cache identity stamped on the most recently opened
	// provider stream (see Agent.stream); cache-miss notices are drained for
	// this key so concurrent agents never steal each other's notices.
	activeCacheKey string
	// remoteCompactAvailableFn memoizes the remote-compaction availability
	// resolution (Codex Phase 2b.1): the gate and model are fixed at
	// construction, so the profile lookup runs at most once instead of on
	// every compaction-policy pass (which holds a.mu and would otherwise
	// re-parse embedded/user variant profiles each turn).
	remoteCompactAvailableFn func() bool
	observers                []observerEntry
	// observerCounter is a per-agent source of unique observer ids used as
	// removal handles (see AddObserver). Per-agent (not package-global) so
	// agents do not share mutable state and tests stay isolated.
	observerCounter uint64
	Output          chan Message

	mu         sync.Mutex
	processing bool
	queue      []string
	cancel     context.CancelFunc
	emitState  OutputState // Track last emitted state for state change events

	// Loop guardrail: tracks how many times each exact tool call (name + input)
	// has been issued in the current turn. Used by MaxToolRepeatTotal.
	turnToolCalls map[string]int
	// turnToolCallCount is kept for metrics/logging only. It is no longer used
	// as a hard per-turn budget cap; that cap is now based on duplicate counts
	// within a rolling window.
	turnToolCallCount int

	// turnHadToolExecution records whether any real (non-synthetic) tool call
	// executed during the current turn. It scopes the empty-response guard: an
	// empty stream is only suspicious when the model produced nothing without
	// any prior tool work. After a tool runs and its result is sent back, an
	// empty follow-up ("done, nothing more to say") is a legitimate turn end.
	turnHadToolExecution bool

	// turnSawContent / turnSawThinking record whether the model produced any
	// visible content or any thinking tokens at any earlier point in the current
	// turn (Issue 13). They let the consecutive-tool-rounds streak treat
	// a model that reasoned earlier in the turn as productive, rather than
	// demanding fresh reasoning in every single round.
	turnSawContent  bool
	turnSawThinking bool

	// streamingToolCalls tracks tool calls that are still being streamed
	// (arguments not yet complete). Maps tool call ID to accumulated partial
	// state so EventToolCallDelta can update the TUI incrementally.
	// streamingToolCallsByIndex is the secondary index keyed by provider
	// content-block index, used to correlate Anthropic input_json_delta
	// events (which carry Delta + ContentIndex but no Partial snapshot).
	// Both are cleared at the start of each stream round via resetStreamRoundState.
	streamingToolCalls        map[string]*partialToolCall
	streamingToolCallsByIndex map[int]*partialToolCall

	// toolCallDeltasThisRound counts EventToolCallDelta fragments received
	// during the current stream round. Logged at round end to confirm whether
	// the active provider actually streams tool-call arguments (P0 diagnostic):
	// a zero count means the widget can only appear at call completion.
	toolCallDeltasThisRound int

	// thinkingBuf accumulates delta thinking tokens from the current assistant
	// response so they can be included in the assistant message when a tool call
	// is handled. DeepSeek requires reasoning_content to be passed back.
	thinkingBuf strings.Builder

	// thinkingDisplayBuf accumulates thinking tokens that have not yet been
	// displayed, suppressing raw tool-call XML that spans multiple deltas.
	// Once the buffer contains no tool-like tags, it is flushed to the UI.
	thinkingDisplayBuf strings.Builder

	// contentBuf accumulates delta content tokens from the current assistant
	// response so they can be included in the assistant message. Without this,
	// content sent before a tool call (or in a text-only response) is lost.
	contentBuf strings.Builder

	// contentDisplayBuf accumulates content tokens held back from display once
	// a tool-call markup signal (tool_call/function/DSML) appears mid-stream,
	// so multi-delta markup (esp. DSML emitted on tool_choice:"none" collapse
	// rounds) is never rendered raw to the user. Flushed (stripped) once the
	// buffer is clean. Text streamed before any signal is emitted live.
	contentDisplayBuf strings.Builder
	// contentMarkupSeen latches true once a tool-call markup signal appears in
	// the content stream, switching display from live deltas to the buffered
	// (markup-suppressing) path for the rest of the response.
	contentMarkupSeen bool

	// turnStatsEmitted tracks whether the provider already sent real token
	// stats during this turn. If true, we skip emitting estimated stats at
	// turn end to avoid double-counting.
	turnStatsEmitted bool

	// lastTurnSilentStop records whether the most recently completed turn ended
	// with a "silent stop": the model produced thinking/reasoning but no visible
	// answer content and no tool calls (a reasoning-token or output limit on the
	// provider side). Set in finalizeStreamTurn, which for such rounds runs only
	// after the in-turn premature-stop steers (classifyPrematureStop) failed to
	// coax an answer out of the model within maxAutoContinuePerTurn attempts.
	// Read by the goal driver (via the LastTurnSilentStop method /
	// SilentStopReporter interface) so it pauses the goal instead of starting
	// another turn into the same limit again.
	lastTurnSilentStop bool

	// turnStartHistoryLen records the length of the history at the start of
	// the current user turn. It is used to identify assistant messages that
	// belong to the current turn so that stream retries can undo only the
	// partial/corrupted message from the failing round, not assistant messages
	// from earlier rounds of the same turn. A negative value means the field
	// has not been initialized (e.g. tests that call undoLastAssistantMessage
	// directly), in which case the function falls back to the last user message.
	turnStartHistoryLen int

	// turnCounter counts completed user turns for the temporal context
	// injection (CX6) message ("turn N"). Incremented once per user input in
	// runInternal. turnStep counts step entries within the current turn
	// ("step M"): reset to 0 in prepareTurn, incremented on every step entry
	// (round 0 via prepareTurn, later rounds, recovery rounds) regardless of
	// whether an injection was due.
	turnCounter int
	turnStep    int

	// providerUsage stores the Usage from EventDone (stream_options.include_usage).
	// When set, emitTurnStats uses these real token counts instead of estimates.
	providerUsage *provider.Usage

	// lastGrossInputTokens is the provider-reported gross prompt size (uncached
	// input + cache read/write) of the last completed request — the ground
	// truth for context-window occupancy, unlike the chars-based estimate that
	// historically under-read tool-heavy sessions by ~20%. lastUsageHistoryLen
	// is len(history) at record time so the estimate for messages appended
	// since can be added on top; lastUsageOutputTokens is the reported output
	// size of that request (used by the length-truncation overflow signal).
	// All three are invalidated whenever history is shrunk or replaced, since
	// the recorded prompt no longer matches the conversation.
	lastGrossInputTokens  int
	lastUsageHistoryLen   int
	lastUsageOutputTokens int

	// lastStopReason is the stop reason of the most recently completed stream
	// round (EndTurn fallback when the provider does not send one). Reset per
	// turn in prepareTurn.
	lastStopReason provider.StopReason

	// steeringSource, when set, is polled between stream rounds (after a round's
	// tool results are appended, before the next runStreamRound) so mid-turn
	// steering is woven into the CURRENT turn instead of delivered as a late,
	// separate turn (steering-lateness; pi parity). Drained messages are
	// appended as user messages at the current history tail — a strict
	// prefix-extension of the prior request (guideline #9 cache-safe).
	steeringSource SteeringSource

	// genStartTime is the wall-clock time the current stream started (window
	// opens in consumeStream). Used to compute output tok/s as a fallback when
	// the provider (LM Studio, llama.cpp, Ollama) omits timing fields.
	genStartTime time.Time
	// genSawEvent reports whether the current stream emitted any mapped event
	// (text/thinking/tool-call delta). Drives the empty-response guard; kept
	// separate from genStartTime, which now opens at stream start for speed
	// timing and so is always set even for empty streams.
	genSawEvent bool
	// genDuration is the wall-clock generation time of the last completed stream
	// (first token → done), used to derive output speed when provider timings
	// are unavailable.
	genDuration time.Duration

	// contextWindow mirrors cfg.Model.ContextWindow and is updated atomically so
	// concurrent readers (e.g. effectiveMaxTokens) can read it without taking mu.
	contextWindow atomic.Int64

	// thinkingStallStart records when the last thinking delta of the current
	// thinking-only phase was received (zero value = not in a thinking-only
	// phase). A stall is declared when NO thinking delta has arrived for
	// longer than ThinkingStallStop — a true stream hang — never because a
	// long but actively-streaming reasoning phase crossed a cumulative
	// duration budget (session export 2026-08-15: a slow locallm streaming
	// reasoning tokens for minutes was killed 5m0s after its FIRST delta
	// despite 2603 subsequent deltas — "no stall but marked as stall").
	// Refreshed on every thinking delta; reset whenever a content token or
	// tool call is received.
	thinkingStallStart time.Time
	// thinkingStallWarned is set after the first stall warning is emitted
	// so we don't flood the event stream.
	thinkingStallWarned bool
	// thinkingStallWarnTimer fires when no thinking delta has arrived for
	// longer than ThinkingStallWarn, emitting the "still thinking" progress
	// warning; thinkingStallStopTimer fires after ThinkingStallStop of
	// silence and stops the turn. Both are re-armed on every thinking delta
	// so only continuous silence trips them, and stopped on content/tool
	// progress or round reset. The stop timer is required because a true
	// no-delta hang delivers no further deltas that could re-evaluate the
	// per-delta check.
	thinkingStallWarnTimer *time.Timer
	thinkingStallStopTimer *time.Timer
	// thinkingStalled is set by the thinking-stall watchdog when the model
	// emits only reasoning tokens for longer than ThinkingStallStop. It is
	// separate from streamLoopDetected: the two guards stop the stream for
	// different reasons, are reported with different errors, and are
	// disabled independently.
	thinkingStalled bool
	// thinkingStallElapsed records how long the thinking-only phase had
	// lasted when the watchdog fired, for the error message and logs.
	thinkingStallElapsed time.Duration

	// bufferedToolCalls collects tool calls during streaming for concurrent
	// execution after the stream ends, rather than executing one at a time.
	bufferedToolCalls []provider.ContentBlock

	// budgetToolCalls records tool call IDs in the current stream that were
	// rejected because the per-turn budget or loop guardrail was exceeded.
	// These calls are still buffered (so they appear in the assistant message's
	// tool_calls array) but are NOT executed — executeBufferedToolCalls
	// substitutes the stored message for their result. Keyed by ToolCallID.
	// An entry with a non-empty string means the call was skipped with that
	// result message; empty or missing means the call was executed normally.
	budgetToolCalls map[string]string

	// bashReuse detects when the model re-runs the same expensive upstream
	// command (e.g. `go test ...`) within a single state epoch while only
	// changing the trailing filter — a wasteful pattern. It resets
	// whenever the state epoch advances (a mutating tool succeeded), so
	// re-running a test after an edit is never flagged. Keyed off bash calls
	// as they are buffered; the flagged call IDs live in bashNearDup.
	bashReuse   *bashReuseTracker
	bashNearDup map[string]bool

	// lastCallKey and consecutiveCount track consecutive identical tool calls
	// (same name + same arguments) across the current turn. When a different
	// call appears (different name or args), consecutiveCount resets to 1.
	// Used for soft-repeat (2x → "already executed") and hard-repeat (3x →
	// loop guard) detection.
	lastCallKey      string
	consecutiveCount int

	// stateEpoch is a monotonically increasing counter bumped every time a
	// state-mutating tool (StateMutator) executes successfully. It implements
	// the state-aware repeat horizon: a repeated exact tool call is only a
	// stall when nothing changed since its previous run. epochAtLastCall
	// records the epoch observed at the most recent buffered call; when it
	// differs from stateEpoch, the repeat horizon (rolling window +
	// consecutive counter) is reset so edit→test→edit cycles never trip the
	// loop guardrail.
	stateEpoch      int
	epochAtLastCall int

	// errStreakTool and errStreak count CONSECUTIVE failing calls of the same
	// tool, regardless of arguments. This catches a model wrestling one tool
	// that keeps erroring with ever-changing inputs (e.g. an interpreter that
	// lacks a feature), which exact tool+args matching cannot see. The streak
	// resets on any success or any different tool. errStreakNudged ensures the
	// guardrail nudges once per episode instead of blocking the tool.
	errStreakTool   string
	errStreak       int
	errStreakNudged bool

	// stopBatchAfterThis is set when a tool result requests that the current
	// tool batch end after this result (e.g. the goal tool setting a non-active
	// status). It causes completeStreamTurn to report no further tool calls
	// even if the model issued some, ending the turn after the results are
	// appended to history.
	stopBatchAfterThis bool

	// toolCollapseNextRound marks the NEXT stream round as the turn's final
	// step (P7): the request carries no tools and tool_choice "none", so the
	// model must produce its summary response text-only. Set by completeStreamTurn
	// when a stop-turn signal is pending; consumed by startStreamRound when it
	// builds the round's provider context.
	toolCollapseNextRound bool

	// collapseStatsPending latches that the CURRENT stream round runs with
	// the P7 text-only collapse: the round's token-stats event carries
	// TextOnlyCollapse so /stats:cache can classify the round's by-design
	// provider-prefix bust as an intentional request-shape change, not an
	// unexpected miss (bugs.md 2026-08-30). Set by startStreamRound /
	// runRecoveryStream when the collapse is applied, cleared at each
	// non-collapsed round start, and consumed by the round's EventTokenStats
	// emission (single-goroutine stream path, same discipline as
	// toolCollapseNextRound).
	collapseStatsPending bool

	// overflowRecoveryAttempted tracks whether an overflow-triggered
	// context compression + stream retry has already been attempted in
	// the current turn. Prevents infinite retry loops when compression
	// cannot free enough space. Reset at the start of each turn in
	// prepareTurn.
	overflowRecoveryAttempted bool

	// lastTurnEnd records when the previous conversation turn finished. It is
	// used by cache-aware compaction (see compaction.go): in-place mutation of
	// old messages (micro compaction / tool_elision) churns the provider prefix
	// cache, so such mutation is deferred until the inter-turn idle gap exceeds
	// MicroCompaction.CacheMissThreshold (i.e. the cache is presumed cold) or
	// usage hits the hard ceiling. Updated under mu in finishProcessing.
	lastTurnEnd time.Time

	// cacheWarmObserved records whether any completed request in this agent
	// reported provider cache reads (CacheReadTokens > 0) — direct evidence the
	// provider prefix cache is hot. It expires the first-turn cold presumption
	// in cacheAssumedCold/cacheAssumedColdForProactive: without it the
	// zero-lastTurnEnd branch presumes cold for the ENTIRE first turn
	// (lastTurnEnd is only written at turn END), failing the gate open and
	// churning a cache that has been hot since round 2. It deliberately does
	// NOT override the idle-gap TTL logic: after a long idle gap the provider
	// cache really has expired, warm history notwithstanding.
	// Set under mu in captureStreamResult; cleared by
	// invalidateContextUsageLocked (history mutation or reset busts the cache,
	// so warmth evidence goes stale together with the recorded prompt size).
	cacheWarmObserved bool

	// lastRoundActivity records when the last provider request completed (any
	// stream round), set under mu in captureStreamResult. The cache gates key
	// off the freshest of this and lastTurnEnd: lastTurnEnd advances only at
	// turn END, so during a long single turn it goes stale and the idle-gap
	// logic would flip the gate cold mid-turn while rounds still complete
	// every few seconds — busting a provably hot cache BELOW the ceiling
	// (prefix-cache bust loop companion defect). lastTurnEnd stays
	// for inter-turn idle bookkeeping. Cleared by Clear.
	lastRoundActivity time.Time

	// lastCacheReadTokens is the previous completed request's cache_read
	// count, kept so the per-request debug log can show cache_read deltas
	// (round-17 anomaly forensics: discriminate provider-side
	// partial eviction from request-shape changes). Cleared by Clear.
	lastCacheReadTokens int

	// lastAssistantHash and assistantRepeatCount detect assistant-message
	// loops where the model emits the same text/thinking across consecutive
	// turns without making progress.
	lastAssistantHash    string
	assistantRepeatCount int

	// streamLoopDetected is set during streaming when the model starts
	// repeating the same substring within a single assistant block. This
	// allows a fast stop before the response grows and wastes context.
	streamLoopDetected bool

	// streamLoopSample holds the repeated sequence captured by
	// checkStreamLoop when the detector fired (one exact repeat unit for the
	// exact-chain detector, the scanned tail for the paraphrase detector;
	// normalized text) so the strike warning/stop messages can show WHAT was
	// judged a loop (runaway-loop visibility). Reset per round
	// alongside streamLoopDetected.
	streamLoopSample string

	// streamLoopStrikes counts stream-loop detections since the last clean
	// streak. Detections below StreamLoopMaxStrikes are soft: the looped
	// round is abandoned, the model is warned with an ephemeral hint, and
	// the turn continues with a fresh round. The strike at the limit stops
	// the turn. Session-scoped: the counter resets only after
	// StreamLoopResetAfter clean messages/tool calls, not between turns.
	streamLoopStrikes int
	// streamLoopCleanCount counts clean messages/tool calls since the last
	// stream-loop strike.
	streamLoopCleanCount int
	// streamLoopStrikeThisRound marks the round in which a strike was
	// registered, so the round itself is not counted as clean activity.
	streamLoopStrikeThisRound bool

	// loopStopped is set when a hard loop guardrail fires so subsequent turns
	// are rejected instead of continuing the runaway exchange. The latch is
	// cleared by ResetLoopStop (genuine new user input / goal resume) and
	// auto-expires after loopStopCooldown — a guardrail must never
	// permanently brick a session (runaway-loop bricking).
	loopStopped bool
	// loopStoppedAt records when the latch was set, for cooldown expiry.
	loopStoppedAt time.Time
	// loopStoppedSample retains the elided repeated sequence that tripped the
	// guardrail so the latched-turn error keeps showing what was judged a
	// loop. Cleared with the rest of the latch state.
	loopStoppedSample string

	// bufferedToolCallCount is the number of tool calls buffered during the
	// current stream. It is reset once the batch is executed so the TUI can
	// render progress like "tool calling (x/Y)" across the stream/tool
	// boundary. EventToolCall consumers should not rely on this for state
	// machine logic.
	bufferedToolCallCount int

	// recentToolCalls tracks the last N tool-call keys used to detect
	// duplicate tool calls within the rolling budget window (MaxToolCalls /
	// ToolCallLimitResetWindow). It is reset at the start of each turn.
	recentToolCalls []string

	// consecutiveToolRounds counts consecutive stream rounds that ended with
	// the model requesting tool calls (finish_reason="tool_calls"). It resets
	// to zero when a round produces a text answer without tool calls. Used by
	// MaxConsecutiveToolRounds to detect "infinite tool-calling loops" where
	// every call has unique inputs and existing repeat guardrails never fire.
	consecutiveToolRounds int
	// toolRoundNudgeFired ensures the forced-answer nudge fires at most once
	// per turn, so legitimate long investigations are interrupted by a single
	// hint rather than a repeating nudge/answer cycle.
	toolRoundNudgeFired bool
	// autoContinueCount tracks how many times this turn auto-continued after a
	// detected premature stop (bounded by maxAutoContinuePerTurn).
	autoContinueCount int
	// lastPersistedGoalReminder is the static goal-reminder text most recently
	// appended to history by persistGoalReminder. The static reminder is
	// byte-identical for a given goal across turns (BuildStaticGoalReminder's
	// contract), so re-appending it every turn just bloats the append-only
	// context (E5, ENHANCE.md): it is re-persisted only when it changes.
	lastPersistedGoalReminder string
	// lastPersistedSticky is the joined sticky-instruction text most recently
	// appended to history by persistStickyInstructions. Re-appending is
	// skipped until the sticky set changes (skill enabled/disabled/edited)
	// or a compression pass invalidates it via InvalidateStickyInstructions.
	lastPersistedSticky string
}
