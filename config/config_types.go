// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/perms"
)

type ModeConfig struct {
	Default      internal.ModeState                            `yaml:"default" json:"default"`
	Defaults     map[internal.MajorMode]internal.AutonomyLevel `yaml:"defaults,omitempty" json:"defaults,omitempty"`
	PlanFilePath string                                        `yaml:"plan_file_path,omitempty" json:"plan_file_path,omitempty"`
}

// Config is the top-level configuration structure. Every leaf key from the
// Goa spec (§3.2) is represented here with yaml tags for serialization.
// RegistryLoaders configures remote provider registries to fetch at startup.
type RegistryLoaders struct {
	Sources []RegistrySource `yaml:"sources,omitempty"`
}

type Config struct {
	ActiveProvider string `yaml:"active_provider"`
	ActiveModel    string `yaml:"active_model"`
	// Deprecated: use Mode.Default instead. Migrated at load time.
	ActiveProfile      string                   `yaml:"active_profile,omitempty"`
	Execution          ExecutionConfig          `yaml:"execution"`
	Mode               ModeConfig               `yaml:"mode"`
	Providers          []ProviderConfig         `yaml:"providers"`
	Models             []ModelConfig            `yaml:"models"`
	MultiAgent         MultiAgentConfig         `yaml:"multi_agent"`
	Memory             MemoryConfig             `yaml:"memory"`
	Skills             SkillsConfig             `yaml:"skills"`
	Tools              ToolsConfig              `yaml:"tools"`
	Completion         CompletionConfig         `yaml:"completion"`
	TUI                TUIConfig                `yaml:"tui"`
	Plugins            PluginsConfig            `yaml:"plugins"`
	Logging            LoggingConfig            `yaml:"logging"`
	Prompts            PromptsConfig            `yaml:"prompts"`
	ThinkingLevels     ThinkingLevelConfig      `yaml:"thinking_levels"`
	ContextCompression ContextCompressionConfig `yaml:"context_compression"`
	TimeContext        TimeContextConfig        `yaml:"time_context"`
	Telegram           TelegramConfig           `yaml:"telegram"`
	Orchestrator       OrchestratorConfig       `yaml:"orchestrator,omitempty"`
	Teams              TeamsConfig              `yaml:"teams,omitempty"`
	Goals              GoalsConfig              `yaml:"goals,omitempty"`
	// Features holds opt-in feature gates (e.g. features.remote_compaction).
	// All gates default off; see FeaturesConfig.
	Features        FeaturesConfig  `yaml:"features,omitempty"`
	Plan            PlanConfig      `yaml:"plan,omitempty"`
	RegistryLoaders RegistryLoaders `yaml:"registry_loaders,omitempty"`
	Permissions     []perms.Rule    `yaml:"permissions,omitempty"`
	// MCP holds Model Context Protocol server definitions keyed by server name.
	// Goa acts as an MCP client: each enabled server's tools are exposed to the
	// agent under the "mcp__<server>__<tool>" namespace.
	MCP map[string]MCPServerConfig `yaml:"mcp,omitempty"`
	// LSP configures language-server integration. Mirrors OpenCode's lsp
	// config: `lsp: false` disables all servers; `lsp.servers.<id>` overrides or
	// disables a specific server; `lsp.servers.<id>` may also define a brand-new
	// custom server with its own command/env/initialization.
	LSP LSPConfig `yaml:"lsp,omitempty"`
	// Aliases maps short user-defined names to command invocations.
	// The value is the full command name (with colon args if needed).
	// Example: n: "session:new" makes /n equivalent to /session:new.
	Aliases   map[string]string `yaml:"aliases,omitempty"`
	FirstRun  bool              `yaml:"-"`
	ConfigDir string            `yaml:"-"`
}

// ExecutionConfig controls execution mode, retries, thresholds, and timeouts.
type ExecutionConfig struct {
	Mode            internal.ExecutionMode `yaml:"mode"`
	Retries         int                    `yaml:"retries"`
	TokenWarning    int                    `yaml:"token_warning"`
	TokenCritical   int                    `yaml:"token_critical"`
	LoopWarning     int                    `yaml:"loop_warning"`
	LoopInterrupt   int                    `yaml:"loop_interrupt"`
	ActivityTimeout string                 `yaml:"activity_timeout"`
	ErrorThreshold  float64                `yaml:"error_threshold"`
	WorktreeMode    internal.WorktreeMode  `yaml:"worktree_mode"`
	// AutoSaveModel is tri-state: nil = inherit from the lower cascade layer
	// (embedded default true). An explicit false opts out of the per-project
	// model pin, falling back to legacy home-only persistence. The pointer is
	// what lets a layer that omits the key preserve the default instead of
	// clobbering it with the bool zero value (bugs.md: /model not pinned to
	// project config on pre-existing installs).
	AutoSaveModel            *bool `yaml:"auto_save_model,omitempty"`
	MaxToolRepeatTotal       int   `yaml:"max_tool_repeat_total"`
	MaxToolRepeatConsecutive int   `yaml:"max_tool_repeat_consecutive"`
	MaxToolCalls             int   `yaml:"max_tool_calls"`
	MaxToolErrorStreak       int   `yaml:"max_tool_error_streak"`
	DisableToolBudget        bool  `yaml:"disable_tool_budget"`
	ToolCallLimitResetWindow int   `yaml:"tool_call_limit_reset_window"`
	MaxStreamRounds          int   `yaml:"max_stream_rounds"`
	MaxConsecutiveToolRounds int   `yaml:"max_consecutive_tool_rounds"`
	AutoHealToolCalls        bool  `yaml:"auto_heal_tool_calls"`
	ThinkingStallWarnSeconds int   `yaml:"thinking_stall_warn_seconds"`
	ThinkingStallStopSeconds int   `yaml:"thinking_stall_stop_seconds"`
	// DisableThinkingLoopDetection/DisableToolLoopDetection persistently
	// disable the corresponding loop detector across sessions. Tri-state:
	// nil (default) = detection on, true = off, false = explicitly on.
	// Temporary session-only overrides use /config:temp:* instead.
	DisableThinkingLoopDetection *bool `yaml:"disable_thinking_loop_detection,omitempty"`
	DisableToolLoopDetection     *bool `yaml:"disable_tool_loop_detection,omitempty"`
	DisableStreamLoopDetection   *bool `yaml:"disable_stream_loop_detection,omitempty"`
	// DisableThinkingStallDetection persistently disables the thinking-stall
	// watchdog (the guard that stops the stream after an extended
	// reasoning-only phase). This is NOT the stream loop detector: it never
	// inspects the text for repetition. Same tri-state semantics as the
	// loop-detection switches above.
	DisableThinkingStallDetection *bool `yaml:"disable_thinking_stall_detection,omitempty"`
	// StreamLoopMaxRepeats is the number of consecutive repeats of the same
	// text block required before the streaming loop detector stops the turn
	// (0 = default 5). Higher values tolerate more deliberate repetition.
	StreamLoopMaxRepeats int `yaml:"stream_loop_max_repeats,omitempty"`
	// StreamLoopMinPeriod is the smallest repeated unit (in characters) the
	// streaming loop detector treats as a loop (0 = default 50). Shorter
	// exact repeats are punctuation/connector noise. Values below 8 are
	// rejected: periods under that floor are never scanned at all.
	StreamLoopMinPeriod int `yaml:"stream_loop_min_period,omitempty"`
	// StreamLoopMaxStrikes is the number of stream-loop detections after
	// which the turn is stopped (0 = default 3). Earlier detections abandon
	// the looped round, warn the model with an ephemeral hint, and re-stream.
	StreamLoopMaxStrikes int `yaml:"stream_loop_max_strikes,omitempty"`
	// StreamLoopResetAfter is the number of clean messages/tool calls (no
	// loop detected) after which the stream-loop strike counter resets to
	// zero (0 = default 10).
	StreamLoopResetAfter int `yaml:"stream_loop_reset_after,omitempty"`
	// RunawayLoopMaxRepeats is the number of consecutive identical assistant
	// responses without progress tolerated before the runaway-loop guardrail
	// stops the session (0 = default 2). Earlier repeats inject a recovery
	// hint and surface a visible warning; reaching the limit latches the
	// session stop. Raise it to give a stalled model more chances to change
	// approach; lower it (minimum 1) to stop at the first repeat.
	RunawayLoopMaxRepeats int `yaml:"runaway_loop_max_repeats,omitempty"`
}

// RetryBackoffConfig configures the exponential-backoff schedule for a
// provider's retry policy. All values are optional; zero falls back to the
// package defaults (initial 1000ms, max 30000ms, jitter 0.25).
type RetryBackoffConfig struct {
	// InitialMS is the base delay for the first retry in milliseconds
	// (doubles per attempt).
	InitialMS int `yaml:"initial_ms,omitempty" json:"initial_ms,omitempty"`
	// MaxMS caps both the local exponential delay and any accepted provider
	// Retry-After, in milliseconds.
	MaxMS int `yaml:"max_ms,omitempty" json:"max_ms,omitempty"`
	// Jitter is the symmetric random multiplier range around one
	// (0.1 = ±10%). Valid range [0, 1].
	Jitter float64 `yaml:"jitter,omitempty" json:"jitter,omitempty"`
}

// RetryPolicyConfig configures per-provider model-request retries, mirroring
// the dsh llm-retry policy. mode is "normal" (finite budget, code-eligible) or
// "always" (retry every model-request failure until cancel).
type RetryPolicyConfig struct {
	// Mode selects normal (default) or always retry behavior.
	Mode string `yaml:"mode,omitempty" json:"mode,omitempty"`
	// MaxRetries is the finite retry budget for normal mode. When unset, the
	// global execution.retries (or provider max_retries) applies.
	MaxRetries int `yaml:"max_retries,omitempty" json:"max_retries,omitempty"`
	// Backoff schedules the delay between attempts.
	Backoff RetryBackoffConfig `yaml:"backoff,omitempty" json:"backoff,omitempty"`
	// Codes restricts normal-mode retries to the listed failure codes
	// (EMPTY_RESPONSE, RATE_LIMIT, SERVER, TIMEOUT, TRANSPORT). Empty uses the
	// default transient set.
	Codes []string `yaml:"codes,omitempty" json:"codes,omitempty"`
}

// ProviderConfig configures a single LLM provider (endpoint + auth).
// Model selection is handled separately via ModelConfig.
type ProviderConfig struct {
	ID         string            `yaml:"id"`
	Name       string            `yaml:"name"`
	Endpoint   string            `yaml:"endpoint"`
	APIKey     string            `yaml:"api_key"`
	Timeout    string            `yaml:"timeout"`
	MaxRetries int               `yaml:"max_retries"`
	Headers    map[string]string `yaml:"headers"`
	UserAgent  string            `yaml:"user_agent"`
	Preferred  bool              `yaml:"preferred"`
	// Deprecated: use ModelConfig instead. Kept during refactor.
	DefaultModel string `yaml:"default_model,omitempty"`

	// Provider identity for agentic compat detection.
	// When empty, inferred from Endpoint / PresetProviders.
	Provider string `yaml:"provider,omitempty"`
	API      string `yaml:"api,omitempty"` // e.g. openai-completions, anthropic-messages

	// BaseURL overrides Endpoint when non-empty.
	BaseURL string `yaml:"base_url,omitempty"`

	// Transport selects the wire protocol: sse (default) or websocket.
	Transport string `yaml:"transport,omitempty"`

	// CacheRetention controls prompt caching: none, short, long.
	CacheRetention string `yaml:"cache_retention,omitempty"`

	// SessionID for cache affinity.
	SessionID string `yaml:"session_id,omitempty"`

	// Extra holds per-provider configuration overrides.
	// These are forwarded to the provider layer at stream time and can customize
	// behavior per provider without code changes.
	// Supported keys:
	//   tool_call_id_max_length: int — truncate tool call IDs (0 = no limit)
	//   normalize_null_descriptions: bool — convert "null" to null in tool schemas
	//   reasoning_key: string — field name for reasoning content (default "reasoning_content")
	//   thinking_extra_body: bool — send thinking config in extra_body
	//   builtin_function_prefix: string — prefix for builtin functions (e.g. "$")
	Extra map[string]any `yaml:"extra,omitempty"`

	// Metadata forwarded to the provider.
	Metadata map[string]string `yaml:"metadata,omitempty"`

	// MaxRetryDelay caps exponential backoff delay.
	MaxRetryDelay string `yaml:"max_retry_delay,omitempty"`

	// RetryPolicy configures per-provider model-request retries (mode,
	// finite budget, backoff, eligible codes). When set, its max_retries beats
	// the global execution.retries and the scalar max_retries above; its
	// backoff overrides MaxRetryDelay. Nil keeps the legacy scalar behavior.
	RetryPolicy *RetryPolicyConfig `yaml:"retry_policy,omitempty" json:"retry_policy,omitempty"`

	// ReasoningEffort sets a default reasoning level for this provider.
	ReasoningEffort string `yaml:"reasoning_effort,omitempty"`

	// ToolResultAsUser overrides whether tool results are sent as user messages.
	// When nil, agentic auto-detects based on provider/model.
	ToolResultAsUser *bool `yaml:"tool_result_as_user,omitempty"`
}

// ModelConfig defines a named model configuration.
// Referenced by ID from active_model and multi_agent roles.
type ModelConfig struct {
	ID            string  `yaml:"id"`
	Name          string  `yaml:"name"`
	ProviderID    string  `yaml:"provider"`
	Model         string  `yaml:"model"` // actual model name sent to API
	Temperature   float64 `yaml:"temperature"`
	MaxTokens     int     `yaml:"max_tokens"`     // max output tokens
	ContextWindow int     `yaml:"context_window"` // context window limit (0 = unknown)

	// API protocol. Empty defaults to the provider's default.
	API string `yaml:"api,omitempty"`

	// Provider name overrides the provider config's provider.
	Provider string `yaml:"provider_name,omitempty"`

	// Reasoning enables thinking/reasoning if the model supports it.
	// Tri-state: nil (default) = enabled, true = explicitly enabled,
	// false = explicitly disabled. When omitted, models are assumed to
	// support reasoning — most models will emit thinking blocks when asked.
	Reasoning *bool `yaml:"reasoning,omitempty"`

	// ThinkingLevel selects the reasoning level: off, minimal, low, medium, high, xhigh.
	ThinkingLevel string `yaml:"thinking_level,omitempty"`

	// ThinkingBudget sets a per-request thinking token budget.
	ThinkingBudget int `yaml:"thinking_budget,omitempty"`

	// ThinkingLevelMap maps Goa's canonical thinking levels to provider-specific
	// token budgets. When empty, DefaultThinkingLevelMap is used.
	ThinkingLevelMap map[string]int `yaml:"thinking_level_map,omitempty"`

	// ThinkingLevelNativeMap maps Goa's canonical thinking levels (off, minimal,
	// low, medium, high, xhigh) to the provider-native values sent on the wire
	// (e.g. "max" for models that only accept low/high/max). This is the direct
	// per-model escape hatch for quick-fixing new or always-thinking models
	// whose accepted levels differ from Goa's canonical set: set the canonical
	// thinking_level you want in the UI, then map it to the native value here.
	// Example:
	//   thinking_level: xhigh
	//   thinking_level_native_map:
	//     xhigh: max
	// It only applies when the resolved variant profile has no map of its own
	// (dedicated profiles like kimi-code already map their levels).
	ThinkingLevelNativeMap map[string]string `yaml:"thinking_level_native_map,omitempty"`

	// InputTypes lists supported input content types (text, image).
	InputTypes []string `yaml:"input_types,omitempty"`

	// Headers are extra HTTP headers for this model.
	Headers map[string]string `yaml:"headers,omitempty"`

	// Compat is a JSON string with provider-specific compat overrides.
	Compat string `yaml:"compat,omitempty"`

	// Pricing sets per-token costs for this model. Zero values = no cost shown.
	Pricing *PricingConfig `yaml:"pricing,omitempty"`

	// CompressOutput controls tool output compression for this model.
	// When nil, the provider-based default is used (enabled for local
	// providers like LM Studio / Ollama, disabled for remote).
	// Explicit true/false overrides the default.
	CompressOutput *bool `yaml:"compress_output,omitempty"`

	// Cache configures whether cache read/write/hit columns are shown.
	Cache *CacheConfig `yaml:"cache,omitempty"`

	// Ephemeral marks memory-only model entries, such as the scratch model
	// created to carry model-scalar CLI overrides when no configured model
	// resolves. Ephemeral entries are never persisted to config files and
	// are hidden from model pickers and completions.
	Ephemeral bool `yaml:"-"`
}

// PricingConfig sets per-token costs for a model, in USD per million tokens.
// Zero values mean no cost is shown (default, graceful degradation).
type PricingConfig struct {
	InputPer1M      float64 `yaml:"input_per_1m"`
	OutputPer1M     float64 `yaml:"output_per_1m"`
	CacheReadPer1M  float64 `yaml:"cache_read_per_1m,omitempty"`
	CacheWritePer1M float64 `yaml:"cache_write_per_1m,omitempty"`
}

// CacheConfig controls whether cache columns are displayed.
type CacheConfig struct {
	Enabled bool `yaml:"enabled"`
}

// PromptsConfig controls prompt loading and overrides.
type PromptsConfig struct {
	Dir string `yaml:"dir"` // override prompt directory (default: .goa/prompts)
}

// CompletionConfig controls command completion behavior.
type CompletionConfig struct {
	MinUsageThreshold int `yaml:"min_usage_threshold" json:"min_usage_threshold"` // min count for "Most Used" (0 = disable)
	MaxMostUsed       int `yaml:"max_most_used" json:"max_most_used"`             // max items in "Most Used" tier
}

// MCP server type identifiers.
