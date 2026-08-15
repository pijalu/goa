// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package agentic provides a Go SDK for building AI agents that interact with
// LLMs and execute tools. The core abstraction is the Agent, which manages
// conversation state, tool execution, and event emission.
package agentic

import (
	"context"
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
	cfg       Config
	reg       ToolLookup
	history   []Message
	observers []observerEntry
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

	// toolSchemaTokens caches the token cost of the registered tool schemas,
	// computed once (the registry is stable for the agent's lifetime). Used by
	// fixedCostTokens to include the per-turn fixed cost in context usage.
	toolSchemaTokensOnce sync.Once
	toolSchemaTokens     int

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
// CJK ≈ 1 token) and is accurate enough for compression decisions without
// adding external dependencies.
type ContextStats struct {
	// Messages is the number of messages in the conversation history.
	Messages int
	// Characters is the total UTF-8 character count of all messages.
	Characters int
	// EstimatedTokens is a rough token count (chars / 4 for English, chars / 2 for CJK).
	EstimatedTokens int
	// MaxTokens is the configured context window limit (0 = unknown/unlimited).
	MaxTokens int
	// UsagePercent is EstimatedTokens / MaxTokens * 100 (0 if MaxTokens is 0).
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

	// ThresholdPercent triggers compression when usage exceeds this
	// percentage of MaxTokens. 0 = default 90.
	// Recommended for inline mode: 75-80.
	//
	// Deprecated: use Thresholds.TriggerPercent. When both are set,
	// ThresholdPercent wins (backwards compatibility).
	ThresholdPercent int

	// Thresholds configures the fill levels at which compression escalates:
	// early cheap maintenance (soft), the main strategy trigger, and the
	// emergency ceiling (hard). See CompressionThresholds.
	Thresholds CompressionThresholds

	// Strategies selects the compression strategy per escalation layer
	// (soft/trigger/hard). See CompressionLayerStrategies. The legacy
	// Strategy field maps to the trigger layer when Strategies.Trigger is
	// unset.
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

	// Strategy selects the compression algorithm.
	// Default: CompressionToolElision.
	Strategy CompressionStrategy

	// PreserveRecentTurns keeps the last N user/assistant/tool turns
	// uncompressed. Default: 2.
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
	// After this many rounds, if the model is still making tool calls, a
	// recovery hint is injected. Set to 0 for unlimited (default).
	MaxStreamRounds int
	// MaxConsecutiveToolRounds is the maximum number of consecutive LLM rounds
	// that end with finish_reason="tool_calls" before a forced-answer hint is
	// injected. Unlike MaxStreamRounds (which counts total rounds including
	// text-only ones), this counter increments only on rounds where the model
	// produced no visible answer and requested more tool calls, catching the
	// "infinite tool-calling loop" where every call has unique inputs. When the
	// limit is reached, the model is told to stop calling tools and answer with
	// what it has. Set to 0 to disable (default: 10).
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

// NewAgent creates a new Agent with the given configuration.
func NewAgent(cfg Config) *Agent {
	// Apply documented micro-compaction defaults when the strategy is micro but
	// the caller left MicroCompaction at zero. Without this, DefaultMicroCompaction
	// Config's values (KeepRecentMessages=20, MinContextRatio=0.5, ...) are
	// silently never applied and microCompactForced reads zero values.
	if cfg.ContextCompression.Strategy == CompressionMicro && cfg.ContextCompression.MicroCompaction == (MicroCompactionConfig{}) {
		cfg.ContextCompression.MicroCompaction = DefaultMicroCompactionConfig
	}
	a := &Agent{
		cfg:           cfg,
		reg:           NewToolRegistry(cfg.Tools),
		Output:        make(chan Message, 10),
		turnToolCalls: make(map[string]int),
		bashReuse:     newBashReuseTracker(),
		bashNearDup:   make(map[string]bool),
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
func (a *Agent) SetHistory(history []Message) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Ensure system prompt is preserved if not present in new history
	hasSystem := false
	for _, m := range history {
		if m.Role == System {
			hasSystem = true
			break
		}
	}

	if !hasSystem && a.cfg.SystemPrompt != "" {
		history = append([]Message{{
			Type:    Content,
			Role:    System,
			Content: a.cfg.SystemPrompt,
		}}, history...)
	}

	a.history = history
	// History was replaced wholesale (session restore): any recorded provider
	// prompt size belongs to the previous conversation, and the sticky dedup
	// state no longer reflects what's in history — re-persist on next turn
	// when the restored conversation lacks the current sticky set.
	a.lastPersistedSticky = ""
	a.invalidateContextUsageLocked()
}

// SetModel replaces the active model for subsequent turns without
// rebuilding the rest of the agent configuration.
func (a *Agent) SetModel(mdl provider.Model) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Model = mdl
	if mdl.ContextWindow > 0 {
		a.contextWindow.Store(int64(mdl.ContextWindow))
	}
}

// SetContextCompression replaces the context compression configuration for
// subsequent turns. Used when the model changes mid-session so the context
// ceiling tracks the new model's context window.
func (a *Agent) SetContextCompression(cfg ContextCompressionConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.ContextCompression = cfg
}

// CompressionConfig returns the current context compression configuration.
func (a *Agent) CompressionConfig() ContextCompressionConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.ContextCompression
}

// SetReasoningEffort replaces the reasoning-effort level for subsequent turns.
func (a *Agent) SetReasoningEffort(effort ReasoningEffort) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.ReasoningEffort = effort
}

// ReasoningEffort returns the current reasoning-effort level.
func (a *Agent) ReasoningEffort() ReasoningEffort {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.ReasoningEffort
}

// SetTools replaces the tool set available to the agent for subsequent turns.
// The updated list takes effect on the next provider call without losing the
// current conversation history.
func (a *Agent) SetTools(tools []Tool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Tools = tools
	a.reg = NewToolRegistry(tools)
}

// Tools returns a copy of the agent's current tool set. Use with SetTools to
// append a tool without clobbering the existing ones.
func (a *Agent) Tools() []Tool {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Tool, len(a.cfg.Tools))
	copy(out, a.cfg.Tools)
	return out
}

// SteeringSource supplies mid-turn steering messages typed by the user while
// the agent is running. It mirrors pi's getSteeringMessages hook. Drain must
// atomically return and remove all currently-pending messages.
type SteeringSource interface {
	Drain() []string
}

// SetSteeringSource wires the queue the agent polls between stream rounds for
// mid-turn steering. Pass nil to disable (tests / single-shot runners).
func (a *Agent) SetSteeringSource(s SteeringSource) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steeringSource = s
}

// IsProcessing reports whether the agent is currently executing a turn
// (including draining its internal queue between turns). The AgentManager
// uses it to report busy state for externally driven turns — e.g. goal
// continuation turns from GoalDriver, which call agent.Run directly and never
// flip the manager's running flag.
func (a *Agent) IsProcessing() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.processing
}

// drainSteeringIntoHistory appends any pending steering messages to history as
// user messages at the current tail. Called between stream rounds: after the
// previous round's assistant/tool messages are appended and before the next
// runStreamRound, so the very next provider request already contains the
// steering. Because the messages are only ever appended at the tail, request
// N+1 stays a strict prefix-extension of request N (guideline #9).
func (a *Agent) drainSteeringIntoHistory() {
	a.mu.Lock()
	src := a.steeringSource
	a.mu.Unlock()
	if src == nil {
		return
	}
	pending := src.Drain()
	for _, text := range pending {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		msg := Message{Type: Content, Role: User, Content: text, Metadata: map[string]string{metaSteeringDrained: "true"}}
		a.mu.Lock()
		a.history = append(a.history, msg)
		a.mu.Unlock()
		a.emitMessage(msg)
	}
}

// InjectSystemMessage appends a system message to the conversation history.
// It is sent to the model on the next turn so the model can be informed of
// runtime changes (for example newly enabled tools) without losing history.
func (a *Agent) InjectSystemMessage(content string) {
	msg := Message{Type: Content, Role: System, Content: content}
	a.mu.Lock()
	a.history = append(a.history, msg)
	a.mu.Unlock()
	a.emitMessage(msg)
}

// metaEphemeral marks a history message as transient: it is sent to the model
// during the turn it is injected but stripped before the next turn so it does
// not pollute future context (e.g. the recovery hint or the repeat-loop nudge).
// The tag lives in Message.Metadata, which migrateMessage does not forward, so
// the model never sees the tag itself (only the message content, during its turn).
const metaEphemeral = "ephemeral"

// metaSteeringDrained marks a user message that was woven into the turn from
// the mid-turn steering queue (drainSteeringIntoHistory). The TUI uses it to
// clear the pending steering bubble and render the consumed text in its place,
// since the bubble would otherwise linger after the queue has been drained.
// Like metaEphemeral, the tag lives in Message.Metadata and is never sent to
// the model (migrateMessage drops Metadata).
const metaSteeringDrained = "steering_drained"

// InjectEphemeralSystemMessage appends a system message that is relevant only
// for the current turn. It is sent to the model now but stripped from history
// at turn end so it is not re-sent (and does not add noise/context) on future
// turns. Use for transient nudges (e.g. the recovery hint); use
// InjectSystemMessage for durable runtime notices (tool changes).
//
// The message is also surfaced to the user as a durable chat bubble so every
// nudge sent to the model is visible and part of the chat history
// the user MUST be aware of nudges). Host control notes (prefixed "[goa-system]")
// are emitted as a system-notification content event, which the app renders as
// a persistent bubble (the same path used for "Error: 401" notices).
func (a *Agent) InjectEphemeralSystemMessage(content string) {
	msg := Message{
		Type:     Content,
		Role:     System,
		Content:  content,
		Metadata: map[string]string{metaEphemeral: "true"},
	}
	a.mu.Lock()
	a.history = append(a.history, msg)
	a.mu.Unlock()

	// Surface the FULL nudge text to the user as a persistent chat bubble
	// (the user MUST be aware of every nudge sent to the model).
	// Previously only a transient EventProgress ("System guardrail…") was shown,
	// hiding the actual content/numbers and leaving the user unable to tell what
	// the model was told. Now every host control note (prefixed "[goa-system]")
	// is emitted as a system-notification content event so it renders as a
	// durable bubble and is part of the chat history.
	if strings.HasPrefix(content, "[goa-system]") {
		a.emitEvent(OutputEvent{
			Type:     EventContent,
			Role:     System,
			Text:     content,
			Metadata: map[string]string{"category": "system-notification"},
		})
	}
}

// stripEphemeralSystemMessages removes ephemeral system messages from history.
// Called at turn end so transient nudges (e.g. the recovery hint) do not persist
// into the next turn's context.
func (a *Agent) stripEphemeralSystemMessages() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.history) == 0 {
		return
	}
	filtered := a.history[:0]
	for _, m := range a.history {
		if m.Role == System && m.Metadata != nil && m.Metadata[metaEphemeral] == "true" {
			continue
		}
		filtered = append(filtered, m)
	}
	a.history = filtered
}

// Model returns the active model configuration.
func (a *Agent) Model() provider.Model {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Model
}

// StreamOptions returns the configured stream options.
func (a *Agent) StreamOptions() provider.StreamOptions {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.StreamOptions
}

// SpillPolicy returns the configured tool-result spill policy (nil when the
// policy is disabled).
func (a *Agent) SpillPolicy() SpillPolicy {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.SpillPolicy
}

// SetStreamOptions replaces the stream options for subsequent turns.
// This updates the API key, headers, timeout, transport, and other provider
// settings. Call after switching providers so the new provider's credentials
// are used on the next turn.
// SetContextWindow updates the model's advertised context window at runtime.
// Used by the host to refresh the loaded context length for local providers
// after the model has finished loading.
func (a *Agent) SetContextWindow(nCtx int) {
	if nCtx <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Model.ContextWindow = nCtx
	a.contextWindow.Store(int64(nCtx))
}

func (a *Agent) SetStreamOptions(opts provider.StreamOptions) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.StreamOptions = opts
	if opts.APIKey != "" {
		a.cfg.APIKey = opts.APIKey
	}
}

// GetHistory returns a copy of the conversation history.
func (a *Agent) GetHistory() []Message {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := make([]Message, len(a.history))
	copy(result, a.history)
	return result
}

// observerEntry pairs an OutputObserver with a unique ID used as an identity
// handle for removal. The id is what AddObserver returns (as a remove handle);
// observer values themselves may be non-comparable function types.
type observerEntry struct {
	obs OutputObserver
	id  uint64
}

// AddObserver registers an observer to receive output events and returns a
// remove handle. Call the returned func exactly once to unregister that
// specific registration. Using a handle (instead of comparing observer values
// via reflect) makes removal reliable even when the same observer is added
// twice or the observer is wrapped in an adapter.
func (a *Agent) AddObserver(o OutputObserver) func() {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.observerCounter++
	id := a.observerCounter
	a.observers = append(a.observers, observerEntry{obs: o, id: id})
	return func() { a.removeObserverByID(id) }
}

// RemoveObserver unregisters a previously added observer by value. It is kept
// for backwards compatibility; new code should prefer the remove handle
// returned by AddObserver. Comparison is identity-based (pointer equality);
// function-typed observers cannot be matched this way (comparing two non-nil
// func values panics), so callers using OutputObserverFunc must retain and use
// the AddObserver handle. RemoveObserver is a no-op when no entry matches.
func (a *Agent) RemoveObserver(o OutputObserver) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, entry := range a.observers {
		if safeObserverEqual(entry.obs, o) {
			a.observers = append(a.observers[:i], a.observers[i+1:]...)
			return
		}
	}
}

// removeObserverByID removes the observer entry with the given id (no-op if
// not found). Called by the remove handle returned from AddObserver.
func (a *Agent) removeObserverByID(id uint64) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for i, entry := range a.observers {
		if entry.id == id {
			a.observers = append(a.observers[:i], a.observers[i+1:]...)
			return
		}
	}
}

// safeObserverEqual reports whether two OutputObserver values are identical by
// pointer/interface equality. Comparing two non-nil function values panics, so
// the comparison is guarded with a recover; such observers are considered
// non-matching (callers must use the AddObserver handle for them). This avoids
// any dependency on reflect.
func safeObserverEqual(a, b OutputObserver) (eq bool) {
	if a == nil || b == nil {
		return a == b
	}
	defer func() { _ = recover() }()
	return a == b
}

func (a *Agent) transitionTo(target OutputState) {
	if a.emitState != target {
		a.emitState = target
		a.emitEvent(OutputEvent{
			Type:  EventStateChange,
			State: target,
		})
	}
}

// Run starts a new conversation turn with the given user input.
// If the agent is already processing, the input is queued and handled
// after the current turn completes. The system prompt is automatically
// prepended on the first call.
//
// Run blocks until the conversation turn completes or the context is cancelled.
func (a *Agent) Run(ctx context.Context, input string) error {
	return a.RunWithMetadata(ctx, input, nil)
}

// RunWithImages starts a new conversation turn with the given user input and
// image attachments. Images are file paths; the provider layer encodes them.
func (a *Agent) RunWithImages(ctx context.Context, input string, images []string) error {
	return a.runInternal(ctx, input, images, nil)
}

// RunWithMetadata starts a new conversation turn with the given user input
// and optional metadata. Metadata is attached to the user message and propagated
// through the Output channel and to all observers, but is NOT sent to the LLM.
//
// This is useful for attaching application-level tags (e.g., category, visibility)
// to individual messages without affecting model context.
func (a *Agent) RunWithMetadata(ctx context.Context, input string, metadata map[string]string) error {
	return a.runInternal(ctx, input, nil, metadata)
}

func (a *Agent) runInternal(ctx context.Context, input string, images []string, metadata map[string]string) error {
	a.mu.Lock()

	// Initialize history with system prompt on first call
	if len(a.history) == 0 {
		sysMsg := Message{
			Type:    Content,
			Role:    System,
			Content: a.cfg.SystemPrompt,
		}
		a.history = append(a.history, sysMsg)
		a.mu.Unlock()
		a.emitMessage(sysMsg)
		a.mu.Lock()
	}

	// If processing, queue and return
	if a.processing {
		a.queue = append(a.queue, input)
		a.mu.Unlock()
		return nil
	}

	a.processing = true
	ctx, cancel := context.WithCancel(ctx)
	a.cancel = cancel
	a.mu.Unlock()

	// Process current and queued inputs
	currentInput := input
	var err error

	for {
		// One turn per user input; the temporal-context reading (CX6) uses
		// the count in its "turn N" label.
		a.turnCounter++
		// Add user message to history and emit event
		userMsg := Message{
			Type:     Content,
			Role:     User,
			Content:  currentInput,
			Images:   images,
			Metadata: metadata,
		}
		a.history = append(a.history, userMsg)
		a.emitMessage(userMsg)

		// Persist the goal context once per turn (kimi-code parity): the
		// reminder becomes ordinary append-only history, so the provider
		// request sequence is strictly append-only and fully prefix-cacheable.
		a.persistGoalReminder()

		// Persist always-on sticky skill instructions under the same
		// contract — deduped, user-role, re-persisted after compression.
		a.persistStickyInstructions()

		// Process one turn
		err = a.processTurn(ctx)
		if err != nil {
			break
		}

		// Check for queued inputs
		a.mu.Lock()
		if len(a.queue) == 0 {
			a.mu.Unlock()
			break
		}
		currentInput = a.queue[0]
		a.queue = a.queue[1:]
		a.mu.Unlock()
	}

	// Cleanup on every exit path (success, error, empty queue). Mark not
	// processing and cancel the per-turn child ctx before discarding the func.
	// Without the cancel() call, every completed turn leaks the cancellable ctx
	// subtree until the *parent* ctx is cancelled (go vet -lostcancel can't see
	// this because cancel is stored in a struct field). The error path
	// previously also left a.processing==true, which made the next Run() queue
	// forever instead of processing.
	a.finishProcessing()

	return err
}

// finishProcessing marks the agent idle and cancels the per-turn child context.
// It must run on every exit path out of runInternal so that the cancellable
// turn ctx (and its subtree) is released and the agent can accept new turns.
// Holding the cancel func without calling it leaks the child ctx tree until the
// caller's parent ctx is cancelled; go vet -lostcancel cannot detect this
// because the func is stored in a struct field rather than a local.
func (a *Agent) finishProcessing() {
	a.mu.Lock()
	a.processing = false
	a.lastTurnEnd = time.Now()
	cancel := a.cancel
	a.cancel = nil
	a.mu.Unlock()
	a.emitEvent(OutputEvent{Type: EventProgress, Text: ""})
	if cancel != nil {
		cancel()
	}
}

// RunAndCollect runs the agent synchronously and collects all text output
// (EventContent) into a single string. Useful for callers that need the
// full response without wiring their own observer, such as sub-agent skill
// execution.
//
// The observer is automatically registered before Run and removed after.
// RunAndCollect runs the agent synchronously and collects all ASSISTANT text
// output (EventContent with Role: Assistant) into a single string.
// System prompt and user messages are excluded. Useful for callers that
// need the full response without wiring their own observer, such as
// sub-agent skill execution or companion testing.
func (a *Agent) RunAndCollect(ctx context.Context, input string) (string, error) {
	var buf strings.Builder
	obs := OutputObserverFunc(func(ev OutputEvent) {
		if ev.Type == EventContent && ev.Role == Assistant && ev.Text != "" {
			buf.WriteString(ev.Text)
		}
	})
	remove := a.AddObserver(obs)
	defer remove()
	err := a.Run(ctx, input)
	return buf.String(), err
}

// Stop cancels any ongoing processing and resets the agent state.
func (a *Agent) Stop() {
	a.mu.Lock()
	if a.cancel != nil {
		a.cancel()
		a.cancel = nil
	}
	a.processing = false
	a.queue = nil
	a.mu.Unlock()
}

// LastTurnSilentStop reports whether the most recently completed turn ended
// with a "silent stop": the model produced thinking/reasoning tokens but no
// visible answer content and no tool calls (a reasoning-token or output limit
// on the provider side). The goal driver uses this to decide whether to pause
// the goal instead of auto-continuing into the same limit.
func (a *Agent) LastTurnSilentStop() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.lastTurnSilentStop
}

func (a *Agent) processTurn(ctx context.Context) error {
	if a.cfg.Model.ID == "" && a.cfg.Model.Api == "" {
		return fmt.Errorf("no model configured: set Config.Model")
	}
	if err := a.checkLoopStopped(); err != nil {
		return err
	}
	if err := a.processTurnWithStream(ctx); err != nil {
		return err
	}
	return a.checkProgressLoop()
}

// loopStopCooldown is how long the runaway-loop latch rejects new turns
// before auto-expiring. A guardrail stops a runaway exchange, never the
// session: genuine recovery paths (ResetLoopStop on new user input or goal
// resume) clear it immediately, and this backstop covers driven paths that
// bypass both (runaway-loop bricking).
const loopStopCooldown = 10 * time.Minute

// checkLoopStopped enforces the runaway-loop latch at turn start. The latch
// auto-expires after loopStopCooldown so no session stays bricked forever.
func (a *Agent) checkLoopStopped() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if !a.loopStopped {
		return nil
	}
	if time.Since(a.loopStoppedAt) >= loopStopCooldown {
		a.cfg.Logger.Log(Info, "Loop guardrail: session stop latch auto-expired after %s", loopStopCooldown)
		a.clearLoopStopLocked()
		return nil
	}
	return fmt.Errorf("session stopped due to a runaway loop%s; please review the conversation and retry", loopEvidenceSuffix(a.loopStoppedSample))
}

// ResetLoopStop clears the runaway-loop latch and repeat counters. It is
// called when a genuine new user message starts a turn (human input, or a
// goal resumed after a runaway pause with a varied recovery prompt): the
// pause/interrupt was the guardrail's stop, and the new input is a deliberate
// attempt to recover — the session must be allowed to proceed.
func (a *Agent) ResetLoopStop() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.loopStopped || a.assistantRepeatCount > 0 {
		a.cfg.Logger.Log(Info, "Loop guardrail: state reset by new user input (latched=%v, repeats=%d)", a.loopStopped, a.assistantRepeatCount)
	}
	a.clearLoopStopLocked()
}

// clearLoopStopLocked resets all runaway-loop guardrail state. Caller must
// hold a.mu.
func (a *Agent) clearLoopStopLocked() {
	a.loopStopped = false
	a.loopStoppedAt = time.Time{}
	a.loopStoppedSample = ""
	a.assistantRepeatCount = 0
	a.lastAssistantHash = ""
}

// checkProgressLoop detects runaway conversations where the assistant repeats
// the same meaningful message across consecutive turns without progress.
// On the first repeat it injects a warning hint AND surfaces a visible TUI
// warning naming the repeated response; on the second repeat it stops the
// session with an error carrying the same evidence (runaway-loop
// visibility: the user must be able to judge whether the loop was real).
//
// The strike only counts when this turn produced a NEW assistant message:
// when the last assistant message predates turnStartHistoryLen (stream
// error, retry, pause), comparing the stale message against itself would
// score a false strike with zero actual repetition.
func (a *Agent) checkProgressLoop() error {
	warnSample, err := a.scanProgressLoop()
	if warnSample != "" {
		// Emitted after scanProgressLoop released a.mu (emitEvent locks it).
		a.emitEvent(OutputEvent{
			Type: EventContent,
			Role: System,
			Text: fmt.Sprintf("Runaway-loop warning: the assistant repeated the same response as the previous turn%s; if it repeats again the session stops.", loopEvidenceSuffix(warnSample)),
			Metadata: map[string]string{
				"category": "system-notification",
			},
		})
	}
	return err
}

// scanProgressLoop evaluates the repeat counters under a.mu. It returns the
// repeated-response sample when the first strike applies (the caller emits
// the visible warning — emitEvent takes a.mu, so it must run unlocked), or
// the terminal guardrail error when the latch trips.
func (a *Agent) scanProgressLoop() (warnSample string, err error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	idx, msg := a.lastAssistantMessageLocked()
	if idx < 0 || idx < a.turnStartHistoryLen {
		return "", nil
	}
	if !a.isMeaningfulAssistantMessage(msg) {
		return "", nil
	}

	hash := a.hashAssistantMessage(msg)
	if hash != a.lastAssistantHash {
		a.lastAssistantHash = hash
		a.assistantRepeatCount = 0
		return "", nil
	}

	a.assistantRepeatCount++
	a.cfg.Logger.Log(Warn, "Loop guardrail: assistant message repeated %d time(s)", a.assistantRepeatCount)

	sample := progressLoopSample(msg)
	if a.assistantRepeatCount == 1 {
		hint := "[goa-system] Your last response was identical to the previous one. Progress has stalled. Change your approach: use a tool, produce different output, or stop and explain the blocker. Repeating the same text will end the session."
		a.history = append(a.history, Message{Type: Content, Role: System, Content: hint})
		return sample, nil
	}

	a.loopStopped = true
	a.loopStoppedAt = time.Now()
	a.loopStoppedSample = elideLoopSample(sample)
	return "", fmt.Errorf("runaway loop detected: the assistant repeated the same response %d consecutive times without progress%s; session stopped", a.assistantRepeatCount+1, loopEvidenceSuffix(sample))
}

// lastAssistantMessageLocked returns the index and value of the most recent
// assistant message in history, or (-1, Message{}) when there is none.
// Caller must hold a.mu.
func (a *Agent) lastAssistantMessageLocked() (int, Message) {
	for i := len(a.history) - 1; i >= 0; i-- {
		if a.history[i].Role == Assistant {
			return i, a.history[i]
		}
	}
	return -1, Message{}
}

// isMeaningfulAssistantMessage reports whether a message should participate in
// progress-loop detection. Any assistant turn — including an empty one with no
// tool calls — can be a stall signal, because the model is supposed to produce
// content, reasoning, or tool calls. Empty turns are treated as meaningful so
// that repeated no-op turns are caught before the context explodes.
func (a *Agent) isMeaningfulAssistantMessage(msg Message) bool {
	return msg.Role == Assistant
}

// hashAssistantMessage builds a simple fingerprint of an assistant message.
func (a *Agent) hashAssistantMessage(msg Message) string {
	return fmt.Sprintf("%s\x00%s\x00%v", strings.TrimSpace(msg.Content), strings.TrimSpace(msg.Thinking), len(msg.ToolCalls))
}

// withToolResultAsUser returns a copy of model with ToolResultAsUser set on its
// OpenAI completions compat.  Existing compat fields are preserved.
func (a *Agent) withToolResultAsUser(model provider.Model, value bool) provider.Model {
	compat, ok := model.Compat.(provider.OpenAICompletionsCompat)
	if !ok {
		compat = provider.OpenAICompletionsCompat{}
	}
	compat.ToolResultAsUser = &value
	model.Compat = compat
	return model
}

func (a *Agent) undoLastAssistantMessage() {
	a.mu.Lock()
	defer a.mu.Unlock()

	start := a.turnStartHistoryLen
	if start < 0 {
		start = 0
		for i := len(a.history) - 1; i >= 0; i-- {
			if a.history[i].Role == User {
				start = i + 1
				break
			}
		}
	}

	for i := len(a.history) - 1; i >= start; i-- {
		if a.history[i].Role == Assistant {
			a.history = a.history[:i]
			return
		}
	}
}

// consumeStream reads events from a stream, buffers tool calls, and
// executes them concurrently after the stream ends.
// Returns true if tool calls were encountered (caller should re-stream).
// a fallback for providers that omit timing fields (LM Studio, llama.cpp, Ollama).
func (a *Agent) Clear() {
	a.mu.Lock()

	if a.cancel != nil {
		a.cancel()
	}

	a.history = nil
	a.queue = nil
	a.processing = false
	a.lastRoundActivity = time.Time{}
	a.lastCacheReadTokens = 0
	a.lastPersistedSticky = ""
	a.clearLoopStopLocked()
	a.invalidateContextUsageLocked()
	a.mu.Unlock()

	// Re-arm provider cache-miss forensics: the post-clear cold start must
	// not be reported as a bust against the cleared conversation's cache.
	provider.ResetCacheForensicsBaseline()

	a.emitEvent(OutputEvent{Type: EventClear})
}

// Compact summarizes the conversation history using the LLM provider
// and replaces it with a condensed version. This is useful for managing
// context window limits in long conversations.
//
// Emits an EventCompact with the summary text.
func (a *Agent) SetBufferedToolCallCountForTest(n int) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.bufferedToolCallCount = n
}

// PolicyConfigForTest exposes the safety-gating fields a sub-agent was built
// with (autonomy, guard, confirm, project dir) so tests can assert policy
// inheritance without reaching into unexported state. Test-only; not part of
// the runtime API.
func (a *Agent) PolicyConfigForTest() (getAutonomy func() internal.AutonomyLevel, getGuard func() perms.GuardConfig, confirm func(context.Context, string, string) (bool, error), projectDir string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.GetAutonomy, a.cfg.GetGuardConfig, a.cfg.ConfirmTool, a.cfg.ProjectDir
}
