// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/hooks"
	"github.com/pijalu/goa/internal/perms"
)

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
