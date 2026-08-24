// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package agentic provides a Go SDK for building AI agents that interact with
// LLMs and execute tools. The core abstraction is the Agent, which manages
// conversation state, tool execution, and event emission.
package agentic

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/hooks"
	"github.com/pijalu/goa/internal/perms"
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
	// provider side). Set in finalizeStreamTurn; read by the goal driver (via the
	// LastTurnSilentStop method / SilentStopReporter interface) so it pauses the
	// goal instead of auto-continuing into the same limit again.
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

// partialToolCall tracks a tool call whose arguments are still being
// streamed from the provider. Used to emit incremental EventToolCall
// updates to observers so the TUI can display partial progress.
type partialToolCall struct {
	toolName     string
	toolCallID   string
	contentIndex int // provider content-block index; correlates nil-Partial deltas (Anthropic)
	argsBuf      strings.Builder
}

// ContextStats holds the current context window usage of an Agent.
//
// EstimatedTokens uses a language-aware heuristic (ASCII ≈ 0.25 tokens,
// CJK ≈ 1 token) and is the conservative full-surface estimate used by the
// reactive safety nets (ceiling enforcement, context-length recovery) and
// cost/event reporting. Proactive compression decisions and occupancy
// displays read ProjectedTokens instead (CX8/P20).
type ContextStats struct {
	// Messages is the number of messages in the conversation history.
	Messages int
	// Characters is the total UTF-8 character count of all messages.
	Characters int
	// EstimatedTokens is a rough token count (chars / 4 for English, chars / 2 for CJK).
	EstimatedTokens int
	// ProjectedTokens is the projected cost of the NEXT request's prompt:
	// the last provider-reported gross prompt size (the anchor) plus the
	// heuristic reprice of every message appended since that request. Only
	// the delta is estimated — the anchor carries the provider's exact count
	// — so the figure reacts the moment content lands (e.g. a large tool
	// result) instead of waiting for the next usage line. Falls back to the
	// full heuristic estimate when no provider usage has been recorded
	// (EstimatedTokens == ProjectedTokens in that case).
	ProjectedTokens int
	// MaxTokens is the configured context window limit (0 = unknown/unlimited).
	MaxTokens int
	// UsagePercent is ProjectedTokens / MaxTokens * 100 (0 if MaxTokens is 0).
	// Occupancy displays and the proactive compaction trigger read the
	// projection (P20 / CX8), never the stale full-surface estimate.
	UsagePercent int
	// AutoMax is true when MaxTokens was inferred from model metadata rather
	// than an explicit user configuration.
	AutoMax bool
}

// CompressionStrategy selects the context compression algorithm.
type CompressionStrategy string

const (
	// CompressionToolElision removes tool call arguments and tool
	// results from older messages, replacing them with brief placeholders.
	// This is the cheapest strategy — no LLM round-trip required.
	CompressionToolElision CompressionStrategy = "tool_elision"

	// CompressionSummarize uses the LLM to summarize a block of
	// older messages into a single assistant message. Most aggressive.
	CompressionSummarize CompressionStrategy = "summarize"

	// CompressionSelective removes the oldest messages entirely,
	// keeping only system prompt + recent turns.
	CompressionSelective CompressionStrategy = "selective"

	// CompressionHybrid first applies tool_elision, then if still
	// over threshold, applies selective removal. Best balance.
	CompressionHybrid CompressionStrategy = "hybrid"

	// CompressionMicro replaces old tool result bodies with a short marker
	// during cache-miss turns, preserving conversation structure while
	// freeing context.
	CompressionMicro CompressionStrategy = "micro"

	// CompressionRemoteCompact replaces history with the server-compacted
	// transcript returned by POST /responses/compact (Codex Phase 2b). It is
	// not a policy-selectable strategy: the summarize slot upgrades to it
	// automatically when the operator gate and the provider capability both
	// allow (2b.1). The constant exists so the provenance events and the
	// EventCompact strategy label can name the remote path distinctly.
	CompressionRemoteCompact CompressionStrategy = "remote_compact"

	// CompressionFreshWindow installs a fresh context window with ZERO
	// summarization calls (Codex TokenBudget mode, 2b.3): history is reset
	// to the system prompt plus the configured recent-turn/last-user
	// preservation tail — no summary is ever requested. It is the cheapest
	// full compaction and IS policy-selectable (any strategy slot may name
	// it); it still runs the normal compaction lifecycle (provenance
	// triple + EventCompact) so hooks and observers see one contract.
	CompressionFreshWindow CompressionStrategy = "fresh_window"
)

// SkillExecutionMode controls how the skill runner executes skills.
type SkillExecutionMode string

const (
	// SkillExecutionModeSubAgent runs each skill in an isolated sub-agent.
	// This is the default and provides full context isolation.
	SkillExecutionModeSubAgent SkillExecutionMode = "subagent"

	// SkillExecutionModeInline returns skill instructions as a tool result
	// within the parent LLM session. The LLM follows the instructions using
	// the parent agent's tools. Context compression is recommended.
	SkillExecutionModeInline SkillExecutionMode = "inline"
)

// TimeContextConfig controls the per-turn temporal context injection (CX6):
// a durable context message carrying a zoned timestamp and elapsed since the
// last reading, injected at model step preparation. A zero value (Enabled
// false) disables injection; RefreshInterval zero or negative injects at
// every eligible step entry.
type TimeContextConfig struct {
	// Enabled turns on per-step temporal context injection.
	Enabled bool
	// TimeZone is the IANA display zone used to format timestamps and
	// reported to the model. Empty uses the local zone.
	TimeZone string
	// RefreshInterval is the minimum wall-clock gap between injections.
	// Zero or negative injects at every eligible step entry.
	RefreshInterval time.Duration
}

// ContextCompressionConfig controls automatic conversation history compression.
//
// A zero value disables automatic compression entirely: every layer is
// opt-in, so the thresholds default to disabled and no reactive recovery
// runs (OnContextError false). Use this to manage context window limits,
// especially important when using inline skill execution mode.
type ContextCompressionConfig struct {
	// MaxTokens is the context window limit. When estimated tokens
	// exceed ThresholdPercent of this, compression is triggered.
	// 0 disables token-based triggering.
	MaxTokens int

	// Thresholds configures the fill levels at which compression escalates:
	// early cheap maintenance (soft) and the emergency ceiling (hard). The
	// model is exactly soft / hard / on-error — there is no trigger layer.
	// See CompressionThresholds.
	Thresholds CompressionThresholds

	// Strategies selects the compression strategy per escalation layer
	// (soft/hard). See CompressionLayerStrategies.
	Strategies CompressionLayerStrategies

	// DisableCacheGate turns the prefix-cache gate off entirely: proactive
	// compression is then never deferred for a presumed-hot provider cache.
	// For models/providers without a prefix cache (or whose cache readings
	// are meaningless) the gate only suppresses compression. Default: false
	// (gate on).
	DisableCacheGate bool

	// OnContextError triggers compression when the LLM returns a
	// context-length / token-limit error. Default: true.
	OnContextError bool

	// OnErrorStrategy selects the strategy applied by the on-context-error
	// recovery (see handleContextError). Empty = CompressionHybrid
	// (tool_elision → selective → summarize as last resort).
	OnErrorStrategy CompressionStrategy

	// MicroCompaction configures the micro compaction strategy.
	// Only used when Strategy == CompressionMicro.
	MicroCompaction MicroCompactionConfig

	// ToolResultPruning configures the pre-compaction tool-result pruner:
	// a model-free pass that runs ahead of summarizeHistory and rewrites
	// over-budget historical tool results in place (head + marker + tail),
	// so a compaction-triggering request can fall back under pressure without
	// an LLM call when pruning alone resolves it. Zero value = defaults.
	ToolResultPruning ToolResultPruningConfig

	// PreserveRecentTurns keeps the last N user/assistant/tool turns
	// uncompressed. Default: 2.
	PreserveRecentTurns int

	// RemoteCompactRetainedBudget bounds the server-compacted replacement
	// transcript: after a remote /responses/compact, the retained tail is
	// trimmed (newest-first) so its estimated tokens stay under this budget
	// (Codex RETAINED_MESSAGE_TOKEN_BUDGET). Zero uses the default
	// (DefaultRemoteCompactRetainedBudget, 64_000); a negative value disables
	// the bound.
	RemoteCompactRetainedBudget int

	// FreshWindow configures the fresh_window (token-budget) compaction
	// strategy (Codex Phase 2b.3): a full window reset with ZERO
	// summarization calls. See FreshWindowConfig.
	FreshWindow FreshWindowConfig
}

// FreshWindowConfig configures the fresh_window compaction strategy: when a
// full-window compaction escalates, install a fresh context window (system
// prompt + preserved recent-turn/last-user tail) instead of paying for an
// LLM summary. The strategy is selected either by enabling this gate (the
// summarize slot upgrades to a fresh window, mirroring the remote_compact
// upgrade) or by naming "fresh_window" on any strategy slot (hard layer,
// on-error, legacy whole-config strategy) — selection implies the gate.
type FreshWindowConfig struct {
	// Enabled opts the fresh-window strategy in as the full-compaction
	// mode: Compact installs a fresh window with zero LLM calls instead of
	// summarizing (remote_compact still wins when available). Default off
	// keeps the local summarize ladder unchanged.
	Enabled bool
	// PreserveRecentTurns bounds the preservation tail kept across the
	// window reset (chain-safe; always keeps at least the last user
	// message). 0 inherits PreserveRecentTurns (which itself defaults to
	// 2); a negative value is clamped to 0 → inherit.
	PreserveRecentTurns int
}

// Config holds the configuration for creating a new Agent.
type Config struct {
	// Model is the LLM model to use. Agent uses provider.Stream() for all
	// LLM interactions.
	Model provider.Model
	// APIKey is the API key for the model provider.
	APIKey string
	// StreamOptions configures the stream request.
	StreamOptions provider.StreamOptions

	// SystemPrompt is the initial system message sent to the LLM.
	SystemPrompt string
	// StreamLoopDisabled, when non-nil and returning true, disables the
	// streaming text loop detector (checkStreamLoop). It is queried per delta
	// so session-level temp overrides (/config:temp:stream_loop_detection:off)
	// and persisted config (execution.disable_stream_loop_detection) take
	// effect mid-stream. Nil means detection is enabled.
	StreamLoopDisabled func() bool
	// StreamLoopMaxRepeats, when non-nil, returns the number of consecutive
	// repeats of the same text block required before the streaming loop
	// detector stops the turn (execution.stream_loop_max_repeats, queried per
	// delta so runtime changes take effect mid-stream). Nil or values < 2
	// mean the default of 5.
	StreamLoopMaxRepeats func() int
	// StreamLoopMinPeriod, when non-nil, returns the smallest repeated unit
	// (in characters) the streaming loop detector treats as a loop
	// (execution.stream_loop_min_period, queried per scan so runtime changes
	// take effect mid-stream). Nil or values below the absolute scan floor
	// mean the default of 50.
	StreamLoopMinPeriod func() int
	// StreamLoopMaxStrikes is the number of stream-loop detections after
	// which the turn is stopped (execution.stream_loop_max_strikes). Earlier
	// detections are soft: the looped round is abandoned, the model is
	// warned with an ephemeral hint, and the turn re-streams. Zero means
	// the default of 3.
	StreamLoopMaxStrikes int
	// StreamLoopResetAfter is the number of clean messages/tool calls (no
	// loop detected) after which the strike counter resets to zero
	// (execution.stream_loop_reset_after). Zero means the default of 10.
	StreamLoopResetAfter int
	// RunawayLoopMaxRepeats is the number of consecutive identical assistant
	// responses without progress tolerated before the runaway-loop guardrail
	// stops the session (execution.runaway_loop_max_repeats). Earlier repeats
	// inject a recovery hint and surface a visible warning. Zero means the
	// default of 2.
	RunawayLoopMaxRepeats int

	// Logger is an optional leveled logger for debugging. If nil, logging is disabled.
	Logger *Logger
	// Tools is the list of tools available to the agent.
	Tools []Tool
	// SkillExecutionMode controls how the skill runner executes skills.
	// Default is SkillExecutionModeSubAgent.
	SkillExecutionMode SkillExecutionMode
	// ContextCompression controls automatic history compression.
	// Zero value disables automatic compression.
	ContextCompression ContextCompressionConfig
	// RemoteCompactionEnabled is the operator opt-in gate for server-side
	// conversation compaction (Codex Phase 2b, POST /responses/compact). It is
	// ANDed with the provider/model's advertised RemoteCompaction capability:
	// remote compaction is only "available" to the compaction policy when both
	// the gate is on AND the endpoint supports it. Default false keeps the
	// local compression ladder unchanged. Detection/gating only — no request
	// logic here (that is 2b.2).
	RemoteCompactionEnabled bool
	// TimeContext controls the per-turn temporal context injection (CX6):
	// a durable context message carrying a zoned timestamp and elapsed
	// since the last reading, injected at model step preparation. Zero
	// value (Enabled false) disables injection.
	TimeContext TimeContextConfig
	// MaxToolRepeatTotal is the maximum number of identical tool calls (same
	// tool + same arguments) allowed within a single turn, including the first
	// call. When the count exceeds this threshold across any streaming rounds
	// in the turn, subsequent identical calls receive a synthetic loop-guardrail
	// result. Default: 10. Set to 0 to disable this total-count guardrail.
	MaxToolRepeatTotal int
	// MaxToolRepeatConsecutive is the maximum number of CONSECUTIVE identical
	// tool calls allowed within a single turn. When a different tool or
	// different arguments appears between calls, the consecutive counter resets.
	// Default: 2 (allow up to 2 consecutive calls; soft-repeat at 2, hard-loop
	// at 3+). Set to 0 to disable.
	MaxToolRepeatConsecutive int
	// MaxToolCalls is the maximum number of duplicate occurrences of the same
	// tool call (same tool + same arguments) allowed within the rolling window
	// of the last ToolCallLimitResetWindow calls. When the count exceeds this
	// threshold, subsequent identical calls receive a synthetic loop-guardrail
	// result telling the model to change approach or use the previous result.
	// Unique calls in the window do not count toward this limit. Default: 0
	// (no rolling-window duplicate guardrail).
	MaxToolCalls int
	// MaxStreamRounds is the maximum number of LLM stream rounds per turn.
	// When the limit is reached a recovery hint is injected so the model
	// answers with what it has. Set to 0 for unlimited — this is also the
	// default, and there is deliberately NO hidden fallback: SDK consumers
	// embedding the agent without an application config get an unbounded
	// (convergence-driven) turn loop unless they set this explicitly. The goa
	// application exposes it as execution.max_stream_rounds.
	MaxStreamRounds int
	// MaxConsecutiveToolRounds is the maximum number of consecutive LLM rounds
	// that end with finish_reason="tool_calls" before a forced-answer hint is
	// injected. Unlike MaxStreamRounds (which counts total rounds including
	// text-only ones), this counter increments only on rounds where the model
	// produced no visible answer and requested more tool calls, catching the
	// "infinite tool-calling loop" where every call has unique inputs. When the
	// limit is reached, the model is told to stop calling tools and answer with
	// what it has. Set to 0 to disable — and NOTE for SDK consumers: 0/unset
	// really means DISABLED (no hidden fallback), so an embedded agent with no
	// application config has no numeric guardrail at all; set this explicitly.
	// The goa application supplies 15 via execution.max_consecutive_tool_rounds.
	MaxConsecutiveToolRounds int
	// DisableToolBudget when true disables the per-turn tool-call budget check
	// entirely, allowing unlimited tool calls per turn. Useful for sessions with
	// many small tool calls. Set via config or session-level toggle.
	DisableToolBudget bool
	// ToolCallLimitResetWindow is the size of the rolling window used to count
	// duplicate tool calls for MaxToolCalls. A call that falls outside this
	// window is no longer counted as a duplicate. Default: 0 (an effective
	// default of max(3*MaxToolCalls, 10) is used).
	ToolCallLimitResetWindow int
	// MaxToolErrorStreak is the maximum number of CONSECUTIVE failing calls of
	// the SAME tool (regardless of arguments) tolerated before a loop
	// guardrail fires once, telling the model to stop and change approach.
	// Unlike the exact-match repeat guards, this catches a model retrying one
	// tool with ever-changing inputs that all fail (e.g. a script interpreter
	// missing a feature). The streak resets on any success or any different
	// tool. Default: 0 (disabled). A value around 4 is recommended.
	MaxToolErrorStreak int
	// ReasoningEffort controls the amount of reasoning the model performs.
	// Values are provider-specific (e.g. "low"/"medium"/"high" for OpenAI,
	// "on"/"off" for Gemma). The zero value ("") omits the parameter.
	ReasoningEffort ReasoningEffort
	// ToolResultAsUser overrides whether tool results are sent as user
	// messages (with XML markers) instead of role: "tool".  When nil, the
	// provider's auto-detected compat setting is used.  Some models (e.g.
	// Gemma via LM Studio, Qwen) require this to associate results with calls.
	ToolResultAsUser *bool
	// GoalStateProvider injects goal context into the system prompt at each
	// turn boundary. Nil disables goal injection.
	GoalStateProvider GoalStateProvider
	// StickyProvider supplies always-on instruction blocks (sticky knowledge
	// skills). Non-empty blocks are persisted into history as user-role
	// messages once per content change — never into the system prompt, which
	// is the provider-cached prefix. Nil disables sticky injection.
	StickyProvider StickyProvider
	// PreTurnProvider supplies additional user-role content delivered at the
	// start of every turn ahead of the user message (e.g. due schedule
	// reminders). Nil disables pre-turn delivery.
	PreTurnProvider PreTurnProvider
	// AutoHealToolCalls enables parsing of malformed XML tool calls emitted
	// by local models.  When true, the agent extracts <tool_call> and
	// <function=name> markup from the assistant text and treats it as a tool
	// call.  Disabled by default.
	AutoHealToolCalls bool
	// ProjectDir is the root of the codebase. It is used by SOLO mode to
	// restrict file-system and shell access to the project directory.
	ProjectDir string
	// SessionID is the current session identifier, forwarded into Claude Code
	// dialect hook payloads (session_id). Empty when no session is active.
	SessionID string
	// GetAutonomy returns the current autonomy level. When non-nil and it
	// returns AutonomySolo, tool calls are validated against the SOLO policy.
	GetAutonomy func() internal.AutonomyLevel
	// GetGuardConfig returns the current mode's access-control rules. When
	// non-nil and the returned config contains rules, tool calls are validated
	// against them before execution.
	GetGuardConfig func() perms.GuardConfig

	// ConfirmTool is called before executing a tool that requires user
	// approval in ask/confirm autonomy modes. It returns true when the tool
	// is allowed to run. When nil or when the current autonomy does not
	// require confirmation, the tool runs without invoking this callback.
	ConfirmTool func(ctx context.Context, toolName, input string) (bool, error)

	// ThinkingStallWarn is the duration of pure thinking (no content or tool
	// calls) before a warning is emitted as an EventProgress. Zero means
	// the default of 60s.
	ThinkingStallWarn time.Duration
	// ThinkingStallStop is the duration of pure thinking before the stream
	// is interrupted. Zero means the default of 300s.
	ThinkingStallStop time.Duration
	// ThinkingStallDisabled, when non-nil and returning true, disables the
	// thinking-stall watchdog (both the warning and the stream stop). It is
	// queried per delta so session-level temp overrides
	// (/config:temp:thinking_stall_detection:off) and persisted config
	// (execution.disable_thinking_stall_detection) take effect mid-stream.
	// Nil means the watchdog is enabled.
	ThinkingStallDisabled func() bool

	// HookEngine executes user-defined lifecycle hooks (beforeTool, afterTool,
	// sessionStart, sessionEnd). When nil, no hooks run.
	HookEngine hooks.AgentHookEngine

	// PluginHookSink receives plugin interception points from the agent loop
	// (message:pre-send, tool-call:pre/post, reply:pre, reply:delta,
	// llm:error). internal/app injects the adapter backed by the plugin hook
	// registry — the same dependency direction as HookEngine (agentic never
	// imports plugins). When nil every seam short-circuits with zero
	// behavior change.
	PluginHookSink PluginHookSink

	// SpillPolicy bounds oversized plain-text tool results (gap CX2): a final
	// result over the configured cap is saved verbatim to the session spill
	// dir and replaced by a budgeted head/tail preview + locator notice.
	// Error results never reach the policy. Nil disables spilling entirely.
	SpillPolicy SpillPolicy

	// InstructionTracker tracks loaded workspace instruction files
	// (AGENTS.md/CLAUDE.md) and their lifecycle changes after successful
	// read/write/edit touches (gap CX5). It is seeded with the baseline
	// context files already rendered into the system prompt. Nil disables
	// the workspace-instruction lifecycle messages.
	InstructionTracker *internal.InstructionTracker

	// AllowEmptyResponse when true disables the empty-response guard that
	// treats a clean stream end with zero events as a transient error.
	// Companion and sub-agents (multiagent pool, orchestration specialists)
	// set this because an empty reply is a valid "nothing to report" outcome.
	// The main interactive agent leaves it false so that provider truncation
	// under load is surfaced instead of silently swallowed.
	AllowEmptyResponse bool
}

func newCacheContextID() string {
	var raw [16]byte
	if _, err := rand.Read(raw[:]); err == nil {
		return hex.EncodeToString(raw[:])
	}
	// crypto/rand failure is exceptionally unlikely; a process-local opaque
	// fallback still prevents accidental sharing between Agent instances.
	return fmt.Sprintf("agent-%p", &raw)
}

// NewAgent creates a new Agent with the given configuration.
func NewAgent(cfg Config) *Agent {
	// Apply documented micro-compaction defaults when the strategy is micro but
	// the caller left MicroCompaction at zero. Without this, DefaultMicroCompaction
	// Config's values (KeepRecentMessages=20, MinContextRatio=0.5, ...) are
	// silently never applied and microCompactForced reads zero values.
	// Apply only when micro is the EXPLICIT soft-layer strategy: the resolved
	// softStrategy defaults to micro even with the soft layer disabled, which
	// would otherwise populate MicroCompaction for non-micro configs.
	if cfg.ContextCompression.Strategies.Soft == CompressionMicro && cfg.ContextCompression.MicroCompaction == (MicroCompactionConfig{}) {
		cfg.ContextCompression.MicroCompaction = DefaultMicroCompactionConfig
	}
	a := &Agent{
		cfg:            cfg,
		cacheContextID: newCacheContextID(),
		reg:            NewToolRegistry(cfg.Tools),
		Output:         make(chan Message, 10),
		turnToolCalls:  make(map[string]int),
		bashReuse:      newBashReuseTracker(),
		bashNearDup:    make(map[string]bool),
		// Memoize remote-compaction availability: the gate + model are fixed
		// for the agent's lifetime, so resolve the profile at most once.
		remoteCompactAvailableFn: sync.OnceValue(func() bool {
			return RemoteCompactionAvailable(cfg.RemoteCompactionEnabled, cfg.Model)
		}),
		// Negative means "not initialized yet"; undoLastAssistantMessage falls
		// back to the last user message in that case (e.g. direct test calls).
		turnStartHistoryLen: -1,
	}
	if cfg.Model.ContextWindow > 0 {
		a.contextWindow.Store(int64(cfg.Model.ContextWindow))
	}
	return a
}

// SetHistory replaces the conversation history.
// Used for session restoration on reconnect.
