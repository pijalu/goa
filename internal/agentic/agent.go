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

	// turnStartHistoryLen records the length of the history at the start of
	// the current user turn. It is used to identify assistant messages that
	// belong to the current turn so that stream retries can undo only the
	// partial/corrupted message from the failing round, not assistant messages
	// from earlier rounds of the same turn. A negative value means the field
	// has not been initialized (e.g. tests that call undoLastAssistantMessage
	// directly), in which case the function falls back to the last user message.
	turnStartHistoryLen int

	// providerUsage stores the Usage from EventDone (stream_options.include_usage).
	// When set, emitTurnStats uses these real token counts instead of estimates.
	providerUsage *provider.Usage

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

	// thinkingStall records when the current thinking-only phase started
	// (zero value = not in a thinking stall). Used to detect models that
	// emit reasoning tokens indefinitely without producing content or tool calls.
	// Reset whenever a content token or tool call is received.
	thinkingStallStart time.Time
	// thinkingStallWarned is set after the first stall warning is emitted
	// so we don't flood the event stream.
	thinkingStallWarned bool

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
	// tool batch end after this result (e.g. UpdateGoal setting a non-active
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

	// lastAssistantHash and assistantRepeatCount detect assistant-message
	// loops where the model emits the same text/thinking across consecutive
	// turns without making progress.
	lastAssistantHash    string
	assistantRepeatCount int

	// streamLoopDetected is set during streaming when the model starts
	// repeating the same substring within a single assistant block. This
	// allows a fast stop before the response grows and wastes context.
	streamLoopDetected bool

	// loopStopped is set when a hard loop guardrail fires so subsequent turns
	// are rejected instead of continuing the runaway exchange.
	loopStopped bool

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

// ContextCompressionConfig controls automatic conversation history compression.
//
// A zero value disables automatic compression. Use this to manage context
// window limits, especially important when using inline skill execution mode.
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

	// OnContextError triggers compression when the LLM returns a
	// context-length / token-limit error. Default: true.
	OnContextError bool

	// MicroCompaction configures the micro compaction strategy.
	// Only used when Strategy == CompressionMicro.
	MicroCompaction MicroCompactionConfig

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
	// AutoHealToolCalls enables parsing of malformed XML tool calls emitted
	// by local models.  When true, the agent extracts <tool_call> and
	// <function=name> markup from the assistant text and treats it as a tool
	// call.  Disabled by default.
	AutoHealToolCalls bool
	// ProjectDir is the root of the codebase. It is used by SOLO mode to
	// restrict file-system and shell access to the project directory.
	ProjectDir string
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
	// is interrupted. Zero means the default of 120s.
	ThinkingStallStop time.Duration

	// HookEngine executes user-defined lifecycle hooks (beforeTool, afterTool,
	// sessionStart, sessionEnd). When nil, no hooks run.
	HookEngine hooks.AgentHookEngine

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

// InjectEphemeralSystemMessage appends a system message that is relevant only
// for the current turn. It is sent to the model now but stripped from history
// at turn end so it is not re-sent (and does not add noise/context) on future
// turns. Use for transient nudges (e.g. the recovery hint); use
// InjectSystemMessage for durable runtime notices (tool changes).
//
// The message is deliberately NOT emitted to observers: it is an internal
// control nudge for the model, and rendering it would confuse the user (the
// model also tends to parrot it as a user-facing "budget" status).
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

func (a *Agent) processTurn(ctx context.Context) error {
	if a.cfg.Model.ID == "" && a.cfg.Model.Api == "" {
		return fmt.Errorf("no model configured: set Config.Model")
	}
	if a.loopStopped {
		return fmt.Errorf("session stopped due to a runaway loop; please review the conversation and retry")
	}
	if err := a.processTurnWithStream(ctx); err != nil {
		return err
	}
	return a.checkProgressLoop()
}

// checkProgressLoop detects runaway conversations where the assistant repeats
// the same meaningful message across consecutive turns without progress.
// On the first repeat it injects a warning hint; on the second repeat it
// stops the session with a clear error.
func (a *Agent) checkProgressLoop() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	msg := a.lastAssistantMessage()
	if !a.isMeaningfulAssistantMessage(msg) {
		return nil
	}

	hash := a.hashAssistantMessage(msg)
	if hash != a.lastAssistantHash {
		a.lastAssistantHash = hash
		a.assistantRepeatCount = 0
		return nil
	}

	a.assistantRepeatCount++
	a.cfg.Logger.Log(Warn, "Loop guardrail: assistant message repeated %d time(s)", a.assistantRepeatCount)

	if a.assistantRepeatCount == 1 {
		hint := "[goa-system] Your last response was identical to the previous one. Progress has stalled. Change your approach: use a tool, produce different output, or stop and explain the blocker. Repeating the same text will end the session."
		a.history = append(a.history, Message{Type: Content, Role: System, Content: hint})
		return nil
	}

	a.loopStopped = true
	return fmt.Errorf("runaway loop detected: the assistant repeated the same response %d consecutive times without progress; session stopped", a.assistantRepeatCount+1)
}

// lastAssistantMessage returns the most recent assistant message in history.
func (a *Agent) lastAssistantMessage() Message {
	for i := len(a.history) - 1; i >= 0; i-- {
		if a.history[i].Role == Assistant {
			return a.history[i]
		}
	}
	return Message{}
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
	a.mu.Unlock()

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
