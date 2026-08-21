// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

type ThinkingLevelConfig struct {
	Default   string `yaml:"default"`
	MainAgent string `yaml:"main_agent"`
	Companion string `yaml:"companion"`
	Planner   string `yaml:"planner"`
	Coder     string `yaml:"coder"`
}

// TimeContextConfig controls the per-turn temporal context injection (CX6):
// a durable context message carrying a zoned timestamp and elapsed-since-last
// reading, injected at model step preparation. The feature is off by default;
// enable it to give the model a clock when interpreting otherwise-unqualified
// dates and times.
type TimeContextConfig struct {
	// Enabled turns on per-step temporal context injection. Off by default.
	Enabled bool `yaml:"enabled"`
	// TimeZone is the IANA display zone used to format timestamps and
	// reported to the model. Empty uses the local zone.
	TimeZone string `yaml:"time_zone,omitempty"`
	// RefreshInterval is the minimum wall-clock gap between readings, as a Go
	// duration string ("60s", "5m"). Empty or "0" injects at every eligible
	// step entry.
	RefreshInterval string `yaml:"refresh_interval,omitempty"`
}

// ContextCompressionConfig controls automatic conversation history compression.
type ContextCompressionConfig struct {
	// Enabled is tri-state: nil = inherit from the lower cascade layer (embedded
	// default: on); an explicit false in a home/project file disables compression.
	Enabled   *bool `yaml:"enabled,omitempty"`
	MaxTokens int   `yaml:"max_tokens"`
	// ThresholdPercent is the legacy single trigger level.
	// Deprecated: use Thresholds.TriggerPercent. When both are set,
	// ThresholdPercent wins (backwards compatibility).
	ThresholdPercent int                                 `yaml:"threshold_percent"`
	Thresholds       CompressionThresholdsConfig         `yaml:"thresholds,omitempty"`
	Strategies       CompressionLayerStrategiesConfig    `yaml:"strategies,omitempty"`
	PerModel         map[string]ModelCompressionOverride `yaml:"per_model,omitempty"`
	OnContextError   bool                                `yaml:"on_context_error"`
	// OnErrorStrategy selects the strategy applied when a context-length
	// error triggers recovery (used when on_context_error is true). Empty =
	// "hybrid" (tool_elision → selective → summarize as last resort).
	OnErrorStrategy string `yaml:"on_error_strategy,omitempty"`
	Strategy        string `yaml:"strategy"`
	// CacheGate controls the prefix-cache gate that defers proactive
	// compression while the provider cache is presumed hot: "on" (default)
	// or "off". Per-model overrides win over the global value. Turn it off
	// for models/providers without a meaningful prefix cache.
	CacheGate           string                  `yaml:"cache_gate,omitempty"`
	PreserveRecentTurns int                     `yaml:"preserve_recent_turns"`
	MicroCompaction     MicroCompactionSettings `yaml:"micro_compaction,omitempty"`
	// ToolResultPruning configures the pre-compaction tool-result pruner
	// (CX1): a model-free pass ahead of summarization that rewrites
	// over-budget historical tool results to head + marker + tail.
	ToolResultPruning ToolResultPruningSettings `yaml:"tool_result_pruning,omitempty"`
	// FreshWindow configures the fresh_window (token-budget) strategy
	// (Codex Phase 2b.3): a full-window compaction with ZERO summarization
	// calls — the window resets to the system prompt plus the preserved
	// recent-turn/last-user tail.
	FreshWindow FreshWindowSettings `yaml:"fresh_window,omitempty"`
}

// CompressionThresholdsConfig holds the fill levels (percent of the effective
// context window) at which compression escalates. Zero fields mean "inherit"
// (from the global section for per-model overrides); 0 in the global section
// DISABLES that layer — there is no implicit engine default-on ceiling (the
// embedded default config sets hard_percent: 95 explicitly).
type CompressionThresholdsConfig struct {
	// SoftPercent is the early maintenance level (0 = disabled).
	SoftPercent int `yaml:"soft_percent,omitempty"`
	// TriggerPercent is the main strategy trigger (0 = disabled).
	TriggerPercent int `yaml:"trigger_percent,omitempty"`
	// HardPercent is the emergency ceiling (0 = disabled; negative values are
	// accepted and also disable the layer — legacy opt-out spelling).
	HardPercent int `yaml:"hard_percent,omitempty"`
}

// CompressionLayerStrategiesConfig holds the per-layer compression strategies.
// Empty fields inherit (from the global section for per-model overrides, from
// the SDK defaults otherwise: soft=micro, trigger=tool_elision, hard=summarize).
// Any strategy is allowed on any layer.
type CompressionLayerStrategiesConfig struct {
	Soft    string `yaml:"soft,omitempty"`
	Trigger string `yaml:"trigger,omitempty"`
	Hard    string `yaml:"hard,omitempty"`
}

// ModelCompressionOverride overrides selected compression settings for one
// model, keyed by models[].id under context_compression.per_model. Zero
// fields inherit the global context_compression values.
type ModelCompressionOverride struct {
	MaxTokens           int                              `yaml:"max_tokens,omitempty"`
	ThresholdPercent    int                              `yaml:"threshold_percent,omitempty"` // Deprecated alias for Thresholds.TriggerPercent.
	Thresholds          CompressionThresholdsConfig      `yaml:"thresholds,omitempty"`
	Strategies          CompressionLayerStrategiesConfig `yaml:"strategies,omitempty"`
	Strategy            string                           `yaml:"strategy,omitempty"`
	CacheGate           string                           `yaml:"cache_gate,omitempty"`
	PreserveRecentTurns int                              `yaml:"preserve_recent_turns,omitempty"`
}

// EnabledValue reports whether context compression is active. Nil means the
// setting was not explicitly provided at this cascade layer; the merged
// default is on (matching the embedded default config).
func (cc ContextCompressionConfig) EnabledValue() bool {
	return cc.Enabled == nil || *cc.Enabled
}

// MicroCompactionSettings holds micro-specific config overrides.
type MicroCompactionSettings struct {
	// Enabled opts micro compaction in as a pre-summarize validation step.
	// It is DISABLED by default so summarize stays the default compaction path.
	// When enabled, micro runs first as a dry-run (no mutation) to validate it
	// can meet the required shrink; summarize always runs on the original
	// history, and micro is only applied for real if summarize overflows.
	Enabled            *bool   `yaml:"enabled,omitempty"`
	KeepRecentMessages int     `yaml:"keep_recent_messages,omitempty"`
	MinContentTokens   int     `yaml:"min_content_tokens,omitempty"`
	CacheMissThreshold string  `yaml:"cache_miss_threshold,omitempty"`
	TruncatedMarker    string  `yaml:"truncated_marker,omitempty"`
	MinContextRatio    float64 `yaml:"min_context_ratio,omitempty"`
}

// FreshWindowSettings holds fresh-window (token-budget) strategy overrides
// (Codex Phase 2b.3). The strategy may also be selected by name
// ("fresh_window") on any strategy slot; this block tunes its behavior and
// can enable it globally (the summarize slot then upgrades to a zero-LLM
// window reset, mirroring the remote_compact upgrade).
type FreshWindowSettings struct {
	// Enabled opts the fresh-window strategy in as the full-compaction
	// mode. Tri-state: nil = inherit the lower cascade layer (default off).
	Enabled *bool `yaml:"enabled,omitempty"`
	// PreserveRecentTurns bounds the preservation tail kept across the
	// window reset (0 = inherit context_compression.preserve_recent_turns,
	// which defaults to 2). The last user message is always kept.
	PreserveRecentTurns int `yaml:"preserve_recent_turns,omitempty"`
}

// FreshWindowEnabled reports whether the fresh-window gate resolves on
// (default false when unset at every cascade layer).
func (cc ContextCompressionConfig) FreshWindowEnabled() bool {
	return cc.FreshWindow.Enabled != nil && *cc.FreshWindow.Enabled
}

// ToolResultPruningSettings holds tool-result pruner config overrides (CX1).
// Character budgets are in Unicode code points; zero fields inherit the
// defaults (threshold 8192, head 4096, tail 1024).
type ToolResultPruningSettings struct {
	// Enabled gates the pre-compaction tool-result pruning pass. DEFAULT OFF
	// (nil/absent = off): at the hard ceiling the configured summarize must
	// run — pre-pruning rewrites historical tool results in place and, when it
	// resolves pressure on its own, skips the summarize entirely. Pruning
	// remains as the summarize-overflow fallback regardless of this flag.
	// Set enabled: true to restore the legacy pre-prune behavior.
	Enabled *bool `yaml:"enabled,omitempty"`
	// ThresholdChars prunes a tool result when its content exceeds this many
	// Unicode code points (default 8192).
	ThresholdChars int `yaml:"threshold_chars,omitempty"`
	// HeadChars is the number of leading Unicode code points retained
	// (default 4096).
	HeadChars int `yaml:"head_chars,omitempty"`
	// TailChars is the number of trailing Unicode code points retained
	// (default 1024).
	TailChars int `yaml:"tail_chars,omitempty"`
}

// PruningEnabled reports whether pre-compaction tool-result pruning is on.
// Default off: absent at every cascade layer = off.
func (s ToolResultPruningSettings) PruningEnabled() bool {
	return s.Enabled != nil && *s.Enabled
}

// TelegramConfig controls the telegram talk style system prompt injection.
type TelegramConfig struct {
	Enabled bool `yaml:"enabled"`
}

// GetThinkingLevel returns the thinking level for a role, falling back to default.
