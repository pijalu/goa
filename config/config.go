// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package config implements the Goa configuration system with a cascade
// loader that merges defaults, home, project, local configs, env vars, and
// CLI flags. It also provides theme loading, first-run detection, and a
// ConfigSaver for persisting runtime changes.
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/perms"
	"github.com/pijalu/goa/internal/role"
	"github.com/pijalu/goa/tools"
	"gopkg.in/yaml.v3"
)

// ModeConfig controls the agent's mode system — major mode, skill stack, and
// autonomy defaults. Replaces the old execution.mode single-value approach.
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
	Telegram           TelegramConfig           `yaml:"telegram"`
	RegistryLoaders    RegistryLoaders          `yaml:"registry_loaders,omitempty"`
	Permissions        []perms.Rule             `yaml:"permissions,omitempty"`
	// Aliases maps short user-defined names to command invocations.
	// The value is the full command name (with colon args if needed).
	// Example: n: "session:new" makes /n equivalent to /session:new.
	Aliases   map[string]string `yaml:"aliases,omitempty"`
	FirstRun  bool              `yaml:"-"`
	ConfigDir string            `yaml:"-"`
}

// ExecutionConfig controls execution mode, retries, thresholds, and timeouts.
type ExecutionConfig struct {
	Mode                     internal.ExecutionMode `yaml:"mode"`
	Retries                  int                    `yaml:"retries"`
	TokenWarning             int                    `yaml:"token_warning"`
	TokenCritical            int                    `yaml:"token_critical"`
	LoopWarning              int                    `yaml:"loop_warning"`
	LoopInterrupt            int                    `yaml:"loop_interrupt"`
	ActivityTimeout          string                 `yaml:"activity_timeout"`
	ErrorThreshold           float64                `yaml:"error_threshold"`
	WorktreeMode             internal.WorktreeMode  `yaml:"worktree_mode"`
	AutoSaveModel            bool                   `yaml:"auto_save_model"`
	MaxToolRepeatTotal       int                    `yaml:"max_tool_repeat_total"`
	MaxToolRepeatConsecutive int                    `yaml:"max_tool_repeat_consecutive"`
	MaxToolCalls             int    `yaml:"max_tool_calls"`
	DisableToolBudget        bool   `yaml:"disable_tool_budget"`
	ToolCallLimitResetWindow int    `yaml:"tool_call_limit_reset_window"`
	MaxStreamRounds          int    `yaml:"max_stream_rounds"`
	AutoHealToolCalls        bool                   `yaml:"auto_heal_tool_calls"`
	ThinkingStallWarnSeconds int                    `yaml:"thinking_stall_warn_seconds"`
	ThinkingStallStopSeconds int                    `yaml:"thinking_stall_stop_seconds"`
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
	Reasoning bool `yaml:"reasoning,omitempty"`

	// ThinkingLevel selects the reasoning level: off, minimal, low, medium, high, xhigh.
	ThinkingLevel string `yaml:"thinking_level,omitempty"`

	// ThinkingBudget sets a per-request thinking token budget.
	ThinkingBudget int `yaml:"thinking_budget,omitempty"`

	// ThinkingLevelMap maps Goa's canonical thinking levels to provider-specific
	// token budgets. When empty, DefaultThinkingLevelMap is used.
	ThinkingLevelMap map[string]int `yaml:"thinking_level_map,omitempty"`

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

// MultiAgentConfig controls multi-agent collaboration settings.
type MultiAgentConfig struct {
	Enabled                bool   `yaml:"enabled"`
	Pattern                string `yaml:"pattern"`
	MaxCompanionCycles     int    `yaml:"max_companion_cycles"`
	CompanionProvider      string `yaml:"companion_provider"`
	CompanionModel         string `yaml:"companion_model"`
	PlannerModel           string `yaml:"planner_model"`
	CoderModel             string `yaml:"coder_model"`
	MessageTimeout         string `yaml:"message_timeout"`
	ShowInterAgentMessages bool   `yaml:"show_inter_agent_messages"`
}

// MemoryConfig controls persistent memory file settings.
type MemoryConfig struct {
	Enabled       bool        `yaml:"enabled"`
	Dir           string      `yaml:"dir"`
	AutoSummarize bool        `yaml:"auto_summarize"`
	Dream         DreamConfig `yaml:"dream,omitempty"`
}

// DreamConfig controls memory consolidation (dream mode).
type DreamConfig struct {
	Enabled          bool    `yaml:"enabled"`
	Auto             bool    `yaml:"auto"`
	Interval         string  `yaml:"interval,omitempty"`
	MinSessions      int     `yaml:"min_sessions,omitempty"`
	Model            string  `yaml:"model,omitempty"`
	Provider         string  `yaml:"provider,omitempty"`
	MaxTokens        int     `yaml:"max_tokens,omitempty"`
	Temperature      float64 `yaml:"temperature,omitempty"`
	OutputDir        string  `yaml:"output_dir,omitempty"`
	ConsolidatedDir  string  `yaml:"consolidated_dir,omitempty"`
	ApplyAfterReview bool    `yaml:"apply_after_review,omitempty"`
}

// SkillsConfig controls the skill system.
type SkillsConfig struct {
	Dirs          []string `yaml:"dirs"`
	Embedded      bool     `yaml:"embedded"`
	ExecutionMode string   `yaml:"execution_mode"` // "inline" (default) or "sub-agent"
}

// ToolsConfig holds tool-specific sub-configurations.
type ToolsConfig struct {
	Bash     BashConfig           `yaml:"bash"`
	Terminal TerminalConfig       `yaml:"terminal"`
	SSH      SSHConfig            `yaml:"ssh"`
	Search         SearchConfig         `yaml:"search"`
	SmartSearch    SmartSearchConfig    `yaml:"smartsearch"`
	Edit     EditConfig           `yaml:"edit"`
	ReadFile tools.FileToolConfig `yaml:"read_file"`
	Write    WriteConfig          `yaml:"write"`
	WebFetch tools.WebFetchConfig `yaml:"webfetch"`
	Enabled  ToolEnabledConfig    `yaml:"enabled"`
}

// TerminalConfig controls the hardened terminal tool.
type TerminalConfig struct {
	Sandbox TerminalSandboxConfig `yaml:"sandbox"`
}

// TerminalSandboxConfig controls sandboxing for the terminal tool.
type TerminalSandboxConfig struct {
	Enabled         bool     `yaml:"enabled"`
	BlockedCommands []string `yaml:"blocked_commands"`
	AllowedCommands []string `yaml:"allowed_commands"`
	TimeoutSeconds  int      `yaml:"timeout_seconds"`
	MaxOutputChars  int      `yaml:"max_output_chars"`
	BypassAllowed   bool     `yaml:"bypass_allowed"`
}

// ToolEnabledConfig controls which optional tools are registered and exposed
// to the model. Tools not listed here follow their built-in defaults.
type ToolEnabledConfig struct {
	BGExec        bool `yaml:"bg_exec"`
	DelegateTo    bool `yaml:"delegate_to"`
	Memento       bool `yaml:"memento"`
	PTYExec       bool `yaml:"pty_exec"`
	RequestReview bool `yaml:"request_review"`
	SSHBash       bool `yaml:"ssh_bash"`
	WebFetch      bool `yaml:"webfetch"`
	// ClarifyDisabled, when true, removes the ask_user_question tool from the
	// model's toolset. It is an inverted flag: the default (false/unset) leaves
	// the tool ENABLED by default, matching the requested behavior. All other
	// flags here are opt-IN; this one is opt-OUT.
	ClarifyDisabled bool `yaml:"clarify_disabled"`
	// set records which fields were explicitly present in YAML so deep merges
	// can override earlier layers only for those keys.
	set map[string]bool
}

// UnmarshalYAML implements yaml.Unmarshaler and records which keys were set.
func (t *ToolEnabledConfig) UnmarshalYAML(node *yaml.Node) error {
	if node.Kind != yaml.MappingNode {
		return nil
	}
	type raw ToolEnabledConfig
	var r raw
	if err := node.Decode(&r); err != nil {
		return err
	}
	*t = ToolEnabledConfig(r)
	t.set = make(map[string]bool)
	for i := 0; i < len(node.Content); i += 2 {
		t.set[node.Content[i].Value] = true
	}
	return nil
}

// SetEnabled sets the named tool flag and marks it as explicitly configured.
func (t *ToolEnabledConfig) SetEnabled(name string, value bool) {
	switch name {
	case "bg_exec":
		t.BGExec = value
	case "delegate_to":
		t.DelegateTo = value
	case "memento":
		t.Memento = value
	case "pty_exec":
		t.PTYExec = value
	case "request_review":
		t.RequestReview = value
	case "ssh_bash":
		t.SSHBash = value
	case "webfetch":
		t.WebFetch = value
	case "clarify_disabled":
		t.ClarifyDisabled = value
	default:
		return
	}
	if t.set == nil {
		t.set = make(map[string]bool)
	}
	t.set[name] = true
}

// ApplyTo copies explicitly set flags from t into target.
func (t *ToolEnabledConfig) ApplyTo(target *ToolEnabledConfig) {
	if t.set == nil || target == nil {
		return
	}
	if t.set["bg_exec"] {
		target.BGExec = t.BGExec
	}
	if t.set["delegate_to"] {
		target.DelegateTo = t.DelegateTo
	}
	if t.set["memento"] {
		target.Memento = t.Memento
	}
	if t.set["pty_exec"] {
		target.PTYExec = t.PTYExec
	}
	if t.set["request_review"] {
		target.RequestReview = t.RequestReview
	}
	if t.set["ssh_bash"] {
		target.SSHBash = t.SSHBash
	}
	if t.set["webfetch"] {
		target.WebFetch = t.WebFetch
	}
	if t.set["clarify_disabled"] {
		target.ClarifyDisabled = t.ClarifyDisabled
	}
	if target.set == nil {
		target.set = make(map[string]bool)
	}
	for k := range t.set {
		target.set[k] = true
	}
}

// EditConfig controls edit tool behavior.
type EditConfig struct {
	tools.FileToolConfig `yaml:",inline"`
	// AllowFuzzOnEdits enables fuzzy matching for edit search/replace.
	// When true, the tool tries exact match first, then trailing whitespace
	// normalization, then full fuzzy whitespace + auto-reindent.
	// When false, only exact match (after CRLF normalization) is used.
	AllowFuzzOnEdits bool `yaml:"allow_fuzz_on_edits"`
}

// WriteConfig controls write tool behavior.
type WriteConfig struct {
	tools.FileToolConfig `yaml:",inline"`
}

// BashConfig controls bash tool behavior.
type BashConfig struct {
	BlockedCommands []string `yaml:"blocked_commands"`
	AllowedCommands []string `yaml:"allowed_commands"`
	EnvMaskPatterns []string `yaml:"env_mask_patterns"`
	// CompressOutput controls tool output compression for the bash tool.
	// nil = auto-detect based on provider (local=on, remote=off).
	// Explicit true/false overrides auto-detect.
	CompressOutput *bool `yaml:"compress_output,omitempty"`
	Jail            bool     `yaml:"jail"`
	// MaxOutputBytes caps the byte size of command output returned to the
	// agent. The tail of the output is kept. Zero defaults to 50KB.
	MaxOutputBytes int `yaml:"max_output_bytes"`
}

// SSHConfig controls SSH bash tool behavior.
type SSHConfig struct {
	Hosts []SSHHostConfig `yaml:"hosts"`
}

// SSHHostConfig configures a single SSH host.
type SSHHostConfig struct {
	ID      string `yaml:"id"`
	Host    string `yaml:"host"`
	Port    int    `yaml:"port"`
	User    string `yaml:"user"`
	KeyFile string `yaml:"key_file"`
}

// SearchConfig controls search tool behavior.
type SearchConfig struct {
	Threads    int      `yaml:"threads"`
	MaxResults int      `yaml:"max_results"`
	Exclude    []string `yaml:"exclude"`
}

// SmartSearchConfig controls the BM25-based smart search tool.
type SmartSearchConfig struct {
	Enabled    bool     `yaml:"enabled"`
	MaxResults int      `yaml:"max_results"`
	MinScore   float64  `yaml:"min_score"`
	Exclude    []string `yaml:"exclude"`
	K1         float64  `yaml:"k1"`
	B          float64  `yaml:"b"`
}

// ModeLineSegmentConfig configures a single mode line segment or side.
type ModeLineSegmentConfig struct {
	Left  []string `yaml:"left,omitempty"`
	Right []string `yaml:"right,omitempty"`
}

// TUIConfig controls the terminal UI appearance and behavior.
type TUIConfig struct {
	Theme          string                `yaml:"theme"`
	Layout         internal.LayoutName   `yaml:"layout"`
	ShowTimestamps bool                  `yaml:"show_timestamps"`
	Transparency   TransparencyConfig    `yaml:"transparency"`
	ModeLine       ModeLineSegmentConfig `yaml:"modeline,omitempty"`
	Spinner        string                `yaml:"spinner"`
}

// TransparencyConfig controls which LLM transparency features are visible.
type TransparencyConfig struct {
	ShowThinking         bool   `yaml:"show_thinking"`
	ShowStreaming        bool   `yaml:"show_streaming"`
	ShowToolCalls        bool   `yaml:"show_tool_calls"`
	ShowTokenStats       bool   `yaml:"show_token_stats"`
	ShowLogs             bool   `yaml:"show_logs"`
	ThinkingPanePosition string `yaml:"thinking_pane_position"`
	HighlightToolInput   bool   `yaml:"highlight_tool_input"`
	ThinkingCollapsed    bool   `yaml:"thinking_collapsed"`
}

// PluginsConfig controls the JS plugin system.
type PluginsConfig struct {
	Dirs    []string `yaml:"dirs"`
	Enabled []string `yaml:"enabled"`
}

// ThinkingLevelConfig controls per-role reasoning effort settings.
type ThinkingLevelConfig struct {
	Default   string `yaml:"default"`
	MainAgent string `yaml:"main_agent"`
	Companion string `yaml:"companion"`
	Planner   string `yaml:"planner"`
	Coder     string `yaml:"coder"`
}

// ContextCompressionConfig controls automatic conversation history compression.
type ContextCompressionConfig struct {
	Enabled             bool                    `yaml:"enabled"`
	MaxTokens           int                     `yaml:"max_tokens"`
	ThresholdPercent    int                     `yaml:"threshold_percent"`
	OnContextError      bool                    `yaml:"on_context_error"`
	Strategy            string                  `yaml:"strategy"`
	PreserveRecentTurns int                     `yaml:"preserve_recent_turns"`
	MicroCompaction     MicroCompactionSettings `yaml:"micro_compaction,omitempty"`
}

// MicroCompactionSettings holds micro-specific config overrides.
type MicroCompactionSettings struct {
	KeepRecentMessages int     `yaml:"keep_recent_messages,omitempty"`
	MinContentTokens   int     `yaml:"min_content_tokens,omitempty"`
	CacheMissThreshold string  `yaml:"cache_miss_threshold,omitempty"`
	TruncatedMarker    string  `yaml:"truncated_marker,omitempty"`
	MinContextRatio    float64 `yaml:"min_context_ratio,omitempty"`
}

// TelegramConfig controls the telegram talk style system prompt injection.
type TelegramConfig struct {
	Enabled bool `yaml:"enabled"`
}

// GetThinkingLevel returns the thinking level for a role, falling back to default.
func (c *Config) GetThinkingLevel(r string) internal.ThinkingLevel {
	level := c.resolveThinkingLevel(r)
	if level == "" {
		level = c.ThinkingLevels.Default
	}
	if level == "" {
		level = "medium"
	}
	return internal.ThinkingLevel(level)
}

func (c *Config) resolveThinkingLevel(r string) string {
	switch r {
	case role.Main, "main_agent":
		return c.mainAgentThinkingLevel()
	case role.Companion:
		return c.companionThinkingLevel()
	case role.Planner:
		return c.ThinkingLevels.Planner
	case role.Coder:
		return c.ThinkingLevels.Coder
	}
	return ""
}

func (c *Config) mainAgentThinkingLevel() string {
	if level := c.ThinkingLevels.MainAgent; level != "" {
		return level
	}
	if m, err := c.GetActiveModelConfig(); err == nil && m.ThinkingLevel != "" {
		return m.ThinkingLevel
	}
	return ""
}

func (c *Config) companionThinkingLevel() string {
	if level := c.ThinkingLevels.Companion; level != "" {
		return level
	}
	return c.modelThinkingLevel(c.MultiAgent.CompanionModel)
}

func (c *Config) modelThinkingLevel(modelID string) string {
	if modelID == "" {
		return ""
	}
	for i := range c.Models {
		if c.Models[i].ID == modelID {
			return c.Models[i].ThinkingLevel
		}
	}
	return ""
}

// GetReasoningEffort returns the agentic ReasoningEffort for the main agent,
// derived from thinking_levels configuration.
func (c *Config) GetReasoningEffort() string {
	return string(c.GetThinkingLevel("main_agent"))
}

// GetToolResultAsUser returns the explicit provider-level override, or nil to
// let agentic auto-detect based on provider/model.
func (c *Config) GetToolResultAsUser() *bool {
	p := c.GetActiveProviderConfig()
	if p != nil && p.ToolResultAsUser != nil {
		return p.ToolResultAsUser
	}
	return nil
}

// GetActiveProviderConfig returns the active provider config, falling back to
// the preferred provider.
func (c *Config) GetActiveProviderConfig() *ProviderConfig {
	if c.ActiveProvider != "" {
		if p := c.GetProviderByID(c.ActiveProvider); p != nil {
			return p
		}
	}
	return c.PreferredProvider()
}

// GetActiveModelConfig returns the active model config.
//
// If ActiveModel is set, that model is returned. Otherwise it falls back to
// the first model whose ProviderID matches the active provider. When no
// ActiveModel is set and multiple models match the active provider, the call
// returns an error so callers cannot silently use an arbitrary model.
func (c *Config) GetActiveModelConfig() (ModelConfig, error) {
	if c.ActiveModel != "" {
		if m := c.GetModelByID(c.ActiveModel); m != nil {
			return *m, nil
		}
		return ModelConfig{}, fmt.Errorf("active_model %q not found", c.ActiveModel)
	}
	p := c.GetActiveProviderConfig()
	if p == nil {
		return ModelConfig{}, fmt.Errorf("no active provider configured")
	}

	var match *ModelConfig
	for i := range c.Models {
		if c.Models[i].ProviderID == p.ID {
			if match != nil {
				return ModelConfig{}, fmt.Errorf("ambiguous active model: multiple models match provider %q; set active_model explicitly", p.ID)
			}
			match = &c.Models[i]
		}
	}
	if match == nil {
		return ModelConfig{}, fmt.Errorf("no model found for provider %q", p.ID)
	}
	return *match, nil
}

// DefaultCompressForProvider reports whether tool output compression should
// be enabled by default for the given provider. Local providers (LM Studio,
// Ollama, and custom endpoints on localhost) default to enabled since they
// often run smaller models with tighter context windows and benefit from the
// token savings. Cloud providers default to disabled — the raw output is
// typically more useful and the LLM has ample context.
//
// The effective compression setting is resolved at tool execution time:
//
//	1. Model-level compress_output (if set) → wins
//	2. Provider auto-detect (this function) → local = on, remote = off
func DefaultCompressForProvider(p *ProviderConfig) bool {
	if p == nil {
		return false
	}
	switch p.Provider {
	case AgenticProviderLMStudio, AgenticProviderOllama:
		return true
	}
	// Custom providers using localhost or 127.0.0.1 endpoints are also local.
	if p.Endpoint != "" {
		if strings.Contains(p.Endpoint, "localhost") || strings.Contains(p.Endpoint, "127.0.0.1") {
			return true
		}
	}
	return false
}

// LoggingConfig controls log output.
type LoggingConfig struct {
	Level     string `yaml:"level"`
	File      string `yaml:"file"`
	TraceKeys bool   `yaml:"trace_keys"`
}

// Validate checks the config for semantic correctness.
func (c *Config) Validate() error {
	var ve internal.ValidationError
	c.validateMode(&ve)
	c.validateWorktree(&ve)
	c.validateActiveProvider(&ve)
	c.validateTimeout(&ve)
	c.validateLoopThresholds(&ve)
	c.validateAgenticProviders(&ve)
	c.validateAgenticModels(&ve)
	c.validateContextCompression(&ve)
	c.validateSkillMode(&ve)
	if ve.HasErrors() {
		return &ve
	}
	return nil
}

func (c *Config) validateMode(ve *internal.ValidationError) {
	switch c.Execution.Mode {
	case internal.ExecutionYolo, internal.ExecutionSolo, internal.ExecutionConfirm, internal.ExecutionReview, "":
		return
	default:
		ve.Add(fmt.Sprintf("execution.mode: must be one of 'yolo', 'solo', 'confirm', or 'review' (got %q)", c.Execution.Mode))
	}
}

func (c *Config) validateWorktree(ve *internal.ValidationError) {
	switch c.Execution.WorktreeMode {
	case internal.WorktreeAlways, internal.WorktreeMultiAgent, "":
		return
	default:
		ve.Add(fmt.Sprintf("execution.worktree_mode: must be 'always' or 'multi_agent' (got %q)", c.Execution.WorktreeMode))
	}
}

func (c *Config) validateActiveProvider(ve *internal.ValidationError) {
	if c.ActiveProvider == "" {
		return
	}
	// Skip provider validation if no providers are configured yet
	if len(c.Providers) == 0 {
		return
	}
	for _, p := range c.Providers {
		if p.ID == c.ActiveProvider {
			return
		}
	}
	ve.Add(fmt.Sprintf("active_provider: provider %q not found in providers list", c.ActiveProvider))
}

func (c *Config) validateTimeout(ve *internal.ValidationError) {
	if c.Execution.ActivityTimeout == "" {
		return
	}
	if _, err := time.ParseDuration(c.Execution.ActivityTimeout); err != nil {
		ve.Add(fmt.Sprintf("execution.activity_timeout: cannot parse %q as duration: %v", c.Execution.ActivityTimeout, err))
	}
}

func (c *Config) validateLoopThresholds(ve *internal.ValidationError) {
	if c.Execution.LoopWarning <= 0 || c.Execution.LoopInterrupt <= 0 {
		return
	}
	if c.Execution.LoopWarning >= c.Execution.LoopInterrupt {
		ve.Add(fmt.Sprintf("execution.loop_warning (%d) must be less than loop_interrupt (%d)",
			c.Execution.LoopWarning, c.Execution.LoopInterrupt))
	}
	// Validate tool repeat thresholds: consecutive must not exceed total.
	if c.Execution.MaxToolRepeatConsecutive > 0 && c.Execution.MaxToolRepeatTotal > 0 &&
		c.Execution.MaxToolRepeatConsecutive > c.Execution.MaxToolRepeatTotal {
		ve.Add(fmt.Sprintf("execution.max_tool_repeat_consecutive (%d) must not exceed execution.max_tool_repeat_total (%d)",
			c.Execution.MaxToolRepeatConsecutive, c.Execution.MaxToolRepeatTotal))
	}
}

func (c *Config) validateAgenticProviders(ve *internal.ValidationError) {
	for _, p := range c.Providers {
		c.validateProviderIdentity(ve, p)
		c.validateProviderTransport(ve, p)
		c.validateProviderCache(ve, p)
		c.validateProviderRetryDelay(ve, p)
	}
}

func (c *Config) validateProviderIdentity(ve *internal.ValidationError, p ProviderConfig) {
	if !IsValidAgenticProvider(p.Provider) {
		ve.Add(fmt.Sprintf("providers.%s.provider: unknown agentic provider %q", p.ID, p.Provider))
	}
	if !IsValidAgenticAPI(p.API) {
		ve.Add(fmt.Sprintf("providers.%s.api: unknown agentic API %q", p.ID, p.API))
	}
}

func (c *Config) validateProviderTransport(ve *internal.ValidationError, p ProviderConfig) {
	if p.Transport == "" || p.Transport == AgenticTransportSSE || p.Transport == AgenticTransportWebSocket {
		return
	}
	ve.Add(fmt.Sprintf("providers.%s.transport: must be %q or %q", p.ID, AgenticTransportSSE, AgenticTransportWebSocket))
}

func (c *Config) validateProviderCache(ve *internal.ValidationError, p ProviderConfig) {
	if p.CacheRetention == "" || p.CacheRetention == AgenticCacheRetentionNone ||
		p.CacheRetention == AgenticCacheRetentionShort || p.CacheRetention == AgenticCacheRetentionLong {
		return
	}
	ve.Add(fmt.Sprintf("providers.%s.cache_retention: must be one of none/short/long", p.ID))
}

func (c *Config) validateProviderRetryDelay(ve *internal.ValidationError, p ProviderConfig) {
	if p.MaxRetryDelay == "" {
		return
	}
	if _, err := time.ParseDuration(p.MaxRetryDelay); err != nil {
		ve.Add(fmt.Sprintf("providers.%s.max_retry_delay: cannot parse %q as duration: %v", p.ID, p.MaxRetryDelay, err))
	}
}

func (c *Config) validateAgenticModels(ve *internal.ValidationError) {
	for _, m := range c.Models {
		if !IsValidAgenticAPI(m.API) {
			ve.Add(fmt.Sprintf("models.%s.api: unknown agentic API %q", m.ID, m.API))
		}
		if !IsValidAgenticProvider(m.Provider) {
			ve.Add(fmt.Sprintf("models.%s.provider_name: unknown agentic provider %q", m.ID, m.Provider))
		}
		if m.ThinkingLevel != "" && m.ThinkingLevel != AgenticThinkingOff && m.ThinkingLevel != AgenticThinkingMinimal && m.ThinkingLevel != AgenticThinkingLow && m.ThinkingLevel != AgenticThinkingMedium && m.ThinkingLevel != AgenticThinkingHigh && m.ThinkingLevel != AgenticThinkingXHigh {
			ve.Add(fmt.Sprintf("models.%s.thinking_level: unknown thinking level %q", m.ID, m.ThinkingLevel))
		}
	}
}

func (c *Config) validateContextCompression(ve *internal.ValidationError) {
	cc := c.ContextCompression
	if !cc.Enabled {
		return
	}
	if cc.Strategy != "" && cc.Strategy != AgenticCompressionToolElision && cc.Strategy != AgenticCompressionSelective && cc.Strategy != AgenticCompressionSummarize && cc.Strategy != AgenticCompressionHybrid && cc.Strategy != AgenticCompressionMicro {
		ve.Add(fmt.Sprintf("context_compression.strategy: unknown strategy %q", cc.Strategy))
	}
	if cc.ThresholdPercent < 0 || cc.ThresholdPercent > 100 {
		ve.Add(fmt.Sprintf("context_compression.threshold_percent: must be 0-100 (got %d)", cc.ThresholdPercent))
	}
}

func (c *Config) validateSkillMode(ve *internal.ValidationError) {
	if c.Skills.ExecutionMode == "" {
		return
	}
	if c.Skills.ExecutionMode != AgenticSkillModeSubAgent && c.Skills.ExecutionMode != AgenticSkillModeInline {
		ve.Add(fmt.Sprintf("skills.execution_mode: must be %q or %q", AgenticSkillModeSubAgent, AgenticSkillModeInline))
	}
}

// GetProviderByID returns the provider config for the given ID, or nil if not found.
func (c *Config) GetProviderByID(id string) *ProviderConfig {
	for i := range c.Providers {
		if c.Providers[i].ID == id {
			return &c.Providers[i]
		}
	}
	return nil
}

// GetModelByID returns the model config for the given ID, or nil if not found.
func (c *Config) GetModelByID(id string) *ModelConfig {
	for i := range c.Models {
		if c.Models[i].ID == id {
			return &c.Models[i]
		}
	}
	return nil
}

// PreferredProvider returns the first provider marked as preferred, or the first provider.
func (c *Config) PreferredProvider() *ProviderConfig {
	if len(c.Providers) == 0 {
		return nil
	}
	for i := range c.Providers {
		if c.Providers[i].Preferred {
			return &c.Providers[i]
		}
	}
	return &c.Providers[0]
}

// DeepMerge merges another Config into this one, following deep-merge rules.
func (c *Config) DeepMerge(other *Config) {
	c.mergeTopLevelScalars(other)
	c.mergeMode(other)
	c.mergeProviders(other)
	c.mergeModels(other)
	c.mergeProfiles(other)
	c.mergeMultiAgent(other)
	c.mergeMemory(other)
	c.mergeSkills(other)
	c.mergeTools(other)
	c.mergeTUI(other)
	c.mergePlugins(other)
	c.mergeLogging(other)
	c.mergePrompts(other)
	c.mergeThinkingLevels(other)
	c.mergeContextCompression(other)
	c.mergeTelegram(other)
	c.mergePermissions(other)
}

// mergeTopLevelScalars overwrites top-level scalar fields from other when set.
func (c *Config) mergeTopLevelScalars(other *Config) {
	if other.ActiveProvider != "" {
		c.ActiveProvider = other.ActiveProvider
	}
	if other.ActiveModel != "" {
		c.ActiveModel = other.ActiveModel
	}
	if other.ActiveProfile != "" {
		c.ActiveProfile = other.ActiveProfile
	}
	mergeExecution(&c.Execution, &other.Execution)
}

// DefaultModeState returns the default ModeState for the config.
// Resolution order:
//  1. Major: mode.default.major (fallback) → "coder"
//  2. Skills: mode.default.skills
//  3. Autonomy: mode.default.autonomy → mode.defaults[major] → built-in fallback
func (c *Config) DefaultModeState() internal.ModeState {
	ms := c.Mode.Default

	if ms.Major == "" {
		ms.Major = internal.MajorCoder
	}

	// If no explicit autonomy set, check mode.defaults for this major
	if ms.Autonomy == "" && c.Mode.Defaults != nil {
		if aut, ok := c.Mode.Defaults[ms.Major]; ok {
			ms.Autonomy = aut
		}
	}

	// If still no autonomy, use built-in defaults based on major
	if ms.Autonomy == "" {
		ms.Autonomy = DefaultAutonomyForMajor(ms.Major)
	}

	return ms
}

// MigrateActiveProfile moves the deprecated ActiveProfile field into
// Mode.Default.Major, overwriting any existing value. It is called once by the
// cascade loader after all layers are merged. The ActiveProfile field is
// cleared so it is not persisted back.
func (c *Config) MigrateActiveProfile() {
	if c.ActiveProfile == "" {
		return
	}
	c.Mode.Default.Major = internal.MajorMode(c.ActiveProfile)
	c.ActiveProfile = ""
}

// ResolvePlanFilePath returns the absolute path to the plan file.
// If Mode.PlanFilePath is set, it is expanded and made absolute relative to
// the project directory (or the current working directory when projectDir is
// empty). The default is `<projectDir>/.goa/plan.md`.
func (c *Config) ResolvePlanFilePath(projectDir string) string {
	p := c.Mode.PlanFilePath
	if p == "" {
		if projectDir == "" {
			return ".goa/plan.md"
		}
		return filepath.Join(projectDir, ".goa", "plan.md")
	}

	p = os.ExpandEnv(p)
	if strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err == nil {
			p = filepath.Join(home, p[2:])
		}
	}
	if filepath.IsAbs(p) {
		return p
	}
	if projectDir != "" {
		return filepath.Join(projectDir, p)
	}
	return p
}

// ActiveMajor returns the active major mode as a string.
func (c *Config) ActiveMajor() string {
	return string(c.DefaultModeState().Major)
}

// SetActiveMajor sets the active major mode (profile).
func (c *Config) SetActiveMajor(major string) {
	c.Mode.Default.Major = internal.MajorMode(major)
}

// DefaultAutonomyForMajor returns the built-in default autonomy for a major.
// Unknown modes default to SOLO so new modes defined only in metadata do not
// require a code change here.
func DefaultAutonomyForMajor(major internal.MajorMode) internal.AutonomyLevel {
	return internal.AutonomySolo
}

// mergeMode merges the ModeConfig section.
func (c *Config) mergeMode(other *Config) {
	// Merge Mode.Default scalar fields (Major, Autonomy) only if set
	if other.Mode.Default.Major != "" {
		c.Mode.Default.Major = other.Mode.Default.Major
	}
	if other.Mode.Default.Autonomy != "" {
		c.Mode.Default.Autonomy = other.Mode.Default.Autonomy
	}
	if other.Mode.Default.Skills != nil {
		c.Mode.Default.Skills = other.Mode.Default.Skills
	}
	// Merge Mode.Defaults map — last-write-wins per key
	if other.Mode.Defaults != nil {
		if c.Mode.Defaults == nil {
			c.Mode.Defaults = make(map[internal.MajorMode]internal.AutonomyLevel)
		}
		for k, v := range other.Mode.Defaults {
			c.Mode.Defaults[k] = v
		}
	}
}

// mergeExecution merges fields from src into dst.
func mergeExecution(dst, src *ExecutionConfig) {
	if src.Mode != "" {
		dst.Mode = src.Mode
	}
	if src.Retries != 0 {
		dst.Retries = src.Retries
	}
	if src.TokenWarning != 0 {
		dst.TokenWarning = src.TokenWarning
	}
	if src.TokenCritical != 0 {
		dst.TokenCritical = src.TokenCritical
	}
	if src.LoopWarning != 0 {
		dst.LoopWarning = src.LoopWarning
	}
	if src.LoopInterrupt != 0 {
		dst.LoopInterrupt = src.LoopInterrupt
	}
	if src.ActivityTimeout != "" {
		dst.ActivityTimeout = src.ActivityTimeout
	}
	if src.ErrorThreshold != 0 {
		dst.ErrorThreshold = src.ErrorThreshold
	}
	if src.WorktreeMode != "" {
		dst.WorktreeMode = src.WorktreeMode
	}
	dst.AutoSaveModel = src.AutoSaveModel
	dst.DisableToolBudget = src.DisableToolBudget
	mergeIntIfSet(&dst.MaxToolRepeatTotal, src.MaxToolRepeatTotal)
	mergeIntIfSet(&dst.MaxToolRepeatConsecutive, src.MaxToolRepeatConsecutive)
	mergeIntIfSet(&dst.MaxToolCalls, src.MaxToolCalls)
	mergeIntIfSet(&dst.ToolCallLimitResetWindow, src.ToolCallLimitResetWindow)
	mergeIntIfSet(&dst.ThinkingStallWarnSeconds, src.ThinkingStallWarnSeconds)
	mergeIntIfSet(&dst.ThinkingStallStopSeconds, src.ThinkingStallStopSeconds)
}

// mergeIntIfSet copies src into dst when src is non-zero.
func mergeIntIfSet(dst *int, src int) {
	if src != 0 {
		*dst = src
	}
}

// mergeProviders merges provider lists by ID — later providers with the same
// ID overwrite earlier ones.
func (c *Config) mergeProviders(other *Config) {
	for _, op := range other.Providers {
		found := false
		for i, cp := range c.Providers {
			if cp.ID == op.ID {
				c.Providers[i] = op
				found = true
				break
			}
		}
		if !found {
			c.Providers = append(c.Providers, op)
		}
	}
}

// mergeProfiles is a no-op now that the profile system has been removed.
// It remains so that callers do not need to change.
func (c *Config) mergeProfiles(other *Config) {
	_ = other
}

// mergeMultiAgent merges the multi-agent config section.
func (c *Config) mergeMultiAgent(other *Config) {
	if other.MultiAgent.Enabled {
		c.MultiAgent.Enabled = true
	}
	if other.MultiAgent.Pattern != "" {
		c.MultiAgent.Pattern = other.MultiAgent.Pattern
	}
	if other.MultiAgent.MaxCompanionCycles != 0 {
		c.MultiAgent.MaxCompanionCycles = other.MultiAgent.MaxCompanionCycles
	}
	if other.MultiAgent.CompanionProvider != "" {
		c.MultiAgent.CompanionProvider = other.MultiAgent.CompanionProvider
	}
	if other.MultiAgent.CompanionModel != "" {
		c.MultiAgent.CompanionModel = other.MultiAgent.CompanionModel
	}
	if other.MultiAgent.PlannerModel != "" {
		c.MultiAgent.PlannerModel = other.MultiAgent.PlannerModel
	}
	if other.MultiAgent.CoderModel != "" {
		c.MultiAgent.CoderModel = other.MultiAgent.CoderModel
	}
	if other.MultiAgent.MessageTimeout != "" {
		c.MultiAgent.MessageTimeout = other.MultiAgent.MessageTimeout
	}
	c.MultiAgent.ShowInterAgentMessages = other.MultiAgent.ShowInterAgentMessages
}

// mergeModels merges the models array by ID.
func (c *Config) mergeModels(other *Config) {
	for _, om := range other.Models {
		found := false
		for i, cm := range c.Models {
			if cm.ID == om.ID {
				c.Models[i] = om
				found = true
				break
			}
		}
		if !found {
			c.Models = append(c.Models, om)
		}
	}
}

// mergePrompts merges the prompts config section.
func (c *Config) mergePrompts(other *Config) {
	if other.Prompts.Dir != "" {
		c.Prompts.Dir = other.Prompts.Dir
	}
}

// mergeMemory merges the memory config section.
func (c *Config) mergeMemory(other *Config) {
	if other.Memory.Enabled {
		c.Memory.Enabled = true
	}
	if other.Memory.Dir != "" {
		c.Memory.Dir = other.Memory.Dir
	}
	c.Memory.AutoSummarize = other.Memory.AutoSummarize
	mergeDream(&c.Memory.Dream, &other.Memory.Dream)
}

func mergeTerminal(dst, src *TerminalConfig) {
	if src.Sandbox.BlockedCommands != nil {
		dst.Sandbox.BlockedCommands = src.Sandbox.BlockedCommands
	}
	if src.Sandbox.AllowedCommands != nil {
		dst.Sandbox.AllowedCommands = src.Sandbox.AllowedCommands
	}
	if src.Sandbox.TimeoutSeconds != 0 {
		dst.Sandbox.TimeoutSeconds = src.Sandbox.TimeoutSeconds
	}
	if src.Sandbox.MaxOutputChars != 0 {
		dst.Sandbox.MaxOutputChars = src.Sandbox.MaxOutputChars
	}
	dst.Sandbox.Enabled = src.Sandbox.Enabled
	dst.Sandbox.BypassAllowed = src.Sandbox.BypassAllowed
}

func mergeDream(dst, src *DreamConfig) {
	if src.Enabled {
		dst.Enabled = true
	}
	if src.Auto {
		dst.Auto = true
	}
	if src.Interval != "" {
		dst.Interval = src.Interval
	}
	if src.MinSessions != 0 {
		dst.MinSessions = src.MinSessions
	}
	if src.Model != "" {
		dst.Model = src.Model
	}
	if src.Provider != "" {
		dst.Provider = src.Provider
	}
	if src.MaxTokens != 0 {
		dst.MaxTokens = src.MaxTokens
	}
	if src.Temperature != 0 {
		dst.Temperature = src.Temperature
	}
	if src.OutputDir != "" {
		dst.OutputDir = src.OutputDir
	}
	if src.ConsolidatedDir != "" {
		dst.ConsolidatedDir = src.ConsolidatedDir
	}
	if src.ApplyAfterReview {
		dst.ApplyAfterReview = true
	}
}

// mergeSkills merges the skills config section.
func (c *Config) mergeSkills(other *Config) {
	c.Skills.Dirs = append(c.Skills.Dirs, other.Skills.Dirs...)
	c.Skills.Dirs = uniqueStrings(c.Skills.Dirs)
	c.Skills.Embedded = other.Skills.Embedded
	if other.Skills.ExecutionMode != "" {
		c.Skills.ExecutionMode = other.Skills.ExecutionMode
	}
}

// uniqueStrings returns a deduplicated copy of the input slice, preserving
// the first occurrence of each string.
func uniqueStrings(input []string) []string {
	seen := make(map[string]struct{}, len(input))
	result := make([]string, 0, len(input))
	for _, s := range input {
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		result = append(result, s)
	}
	return result
}

// mergeTools merges the tools config section.
func (c *Config) mergeTools(other *Config) {
	if other.Tools.Bash.BlockedCommands != nil {
		c.Tools.Bash.BlockedCommands = other.Tools.Bash.BlockedCommands
	}
	if other.Tools.Bash.AllowedCommands != nil {
		c.Tools.Bash.AllowedCommands = other.Tools.Bash.AllowedCommands
	}
	if other.Tools.Bash.EnvMaskPatterns != nil {
		c.Tools.Bash.EnvMaskPatterns = other.Tools.Bash.EnvMaskPatterns
	}
	if other.Tools.Bash.MaxOutputBytes != 0 {
		c.Tools.Bash.MaxOutputBytes = other.Tools.Bash.MaxOutputBytes
	}
	if other.Tools.Bash.CompressOutput != nil {
		c.Tools.Bash.CompressOutput = other.Tools.Bash.CompressOutput
	}
	mergeTerminal(&c.Tools.Terminal, &other.Tools.Terminal)
	if other.Tools.SSH.Hosts != nil {
		c.Tools.SSH.Hosts = other.Tools.SSH.Hosts
	}
	if other.Tools.Search.Threads != 0 {
		c.Tools.Search.Threads = other.Tools.Search.Threads
	}
	if other.Tools.Search.MaxResults != 0 {
		c.Tools.Search.MaxResults = other.Tools.Search.MaxResults
	}
	if other.Tools.Search.Exclude != nil {
		c.Tools.Search.Exclude = other.Tools.Search.Exclude
	}
	mergeSmartSearch(&c.Tools.SmartSearch, &other.Tools.SmartSearch)
	mergeWebFetch(&c.Tools.WebFetch, &other.Tools.WebFetch)
	mergeReadFile(&c.Tools.ReadFile, &other.Tools.ReadFile)
	mergeEditFile(&c.Tools.Edit, &other.Tools.Edit)
	mergeWriteFile(&c.Tools.Write, &other.Tools.Write)
	other.Tools.Enabled.ApplyTo(&c.Tools.Enabled)
}

// mergeReadFile merges the read_file tool config, preserving the default-on
// fuzzy_match value when the source config does not set it.
func mergeReadFile(dst, src *tools.FileToolConfig) {
	if src.FuzzyMatch != nil {
		dst.FuzzyMatch = src.FuzzyMatch
	}
}

// mergeEditFile merges the edit tool config, preserving the default-on
// fuzzy_match value when the source config does not set it.
func mergeEditFile(dst, src *EditConfig) {
	if src.FuzzyMatch != nil {
		dst.FuzzyMatch = src.FuzzyMatch
	}
	if src.AllowFuzzOnEdits {
		dst.AllowFuzzOnEdits = true
	}
}

// mergeWriteFile merges the write tool config, preserving the default-on
// fuzzy_match value when the source config does not set it.
func mergeWriteFile(dst, src *WriteConfig) {
	if src.FuzzyMatch != nil {
		dst.FuzzyMatch = src.FuzzyMatch
	}
}

// mergeSmartSearch merges the smartsearch config fields.
func mergeSmartSearch(dst, src *SmartSearchConfig) {
	if src.MaxResults != 0 {
		dst.MaxResults = src.MaxResults
	}
	if src.MinScore != 0 {
		dst.MinScore = src.MinScore
	}
	if src.Exclude != nil {
		dst.Exclude = src.Exclude
	}
	if src.K1 != 0 {
		dst.K1 = src.K1
	}
	if src.B != 0 {
		dst.B = src.B
	}
	dst.Enabled = src.Enabled
}

// mergeWebFetch merges the webfetch tool config, preserving defaults for
// unset scalar fields so embedded defaults are not zeroed by a project
// config that only touches other tools. Boolean flags are left at their
// default unless explicitly set to true; disabling is handled through
// tools.enabled.webfetch.
func mergeWebFetch(dst, src *tools.WebFetchConfig) {
	mergeNonZeroScalars(reflect.ValueOf(dst).Elem(), reflect.ValueOf(src).Elem())
}

// mergeNonZeroScalars copies non-zero exported scalar, slice and string fields
// from src into dst. It recurses into nested structs so callers can keep
// per-section merge functions small.
func mergeNonZeroScalars(dst, src reflect.Value) {
	t := dst.Type()
	for i := 0; i < dst.NumField(); i++ {
		ft := t.Field(i)
		if !ft.IsExported() {
			continue
		}
		df := dst.Field(i)
		sf := src.Field(i)
		if ft.Type.Kind() == reflect.Struct {
			mergeNonZeroScalars(df, sf)
			continue
		}
		if !sf.IsZero() {
			df.Set(sf)
		}
	}
}

// mergeTUI merges the TUI config section.
func (c *Config) mergeTUI(other *Config) {
	if other.TUI.Theme != "" {
		c.TUI.Theme = other.TUI.Theme
	}
	if other.TUI.Layout != "" {
		c.TUI.Layout = other.TUI.Layout
	}
	c.TUI.ShowTimestamps = other.TUI.ShowTimestamps
	mergeTransparency(&c.TUI.Transparency, &other.TUI.Transparency)
	if other.TUI.ModeLine.Left != nil {
		c.TUI.ModeLine.Left = other.TUI.ModeLine.Left
	}
	if other.TUI.ModeLine.Right != nil {
		c.TUI.ModeLine.Right = other.TUI.ModeLine.Right
	}
	if other.TUI.Spinner != "" {
		c.TUI.Spinner = other.TUI.Spinner
	}
}

// mergeTransparency merges transparency config fields.
func mergeTransparency(dst, src *TransparencyConfig) {
	if src.ShowThinking {
		dst.ShowThinking = true
	}
	if src.ShowStreaming {
		dst.ShowStreaming = true
	}
	if src.ShowToolCalls {
		dst.ShowToolCalls = true
	}
	if src.ShowTokenStats {
		dst.ShowTokenStats = true
	}
	if src.ShowLogs {
		dst.ShowLogs = true
	}
	if src.ThinkingPanePosition != "" {
		dst.ThinkingPanePosition = src.ThinkingPanePosition
	}
	dst.HighlightToolInput = src.HighlightToolInput
	dst.ThinkingCollapsed = src.ThinkingCollapsed
}

// mergePlugins merges the plugins config section.
func (c *Config) mergePlugins(other *Config) {
	if other.Plugins.Dirs != nil {
		c.Plugins.Dirs = other.Plugins.Dirs
	}
	if other.Plugins.Enabled != nil {
		c.Plugins.Enabled = other.Plugins.Enabled
	}
}

// mergeLogging merges the logging config section.
func (c *Config) mergeLogging(other *Config) {
	if other.Logging.Level != "" {
		c.Logging.Level = other.Logging.Level
	}
	if other.Logging.File != "" {
		c.Logging.File = other.Logging.File
	}
	c.Logging.TraceKeys = c.Logging.TraceKeys || other.Logging.TraceKeys
}

// mergeThinkingLevels merges the thinking levels config section.
func (c *Config) mergeThinkingLevels(other *Config) {
	if other.ThinkingLevels.Default != "" {
		c.ThinkingLevels.Default = other.ThinkingLevels.Default
	}
	if other.ThinkingLevels.MainAgent != "" {
		c.ThinkingLevels.MainAgent = other.ThinkingLevels.MainAgent
	}
	if other.ThinkingLevels.Companion != "" {
		c.ThinkingLevels.Companion = other.ThinkingLevels.Companion
	}
	if other.ThinkingLevels.Planner != "" {
		c.ThinkingLevels.Planner = other.ThinkingLevels.Planner
	}
	if other.ThinkingLevels.Coder != "" {
		c.ThinkingLevels.Coder = other.ThinkingLevels.Coder
	}
}

func (c *Config) mergeContextCompression(other *Config) {
	cc := other.ContextCompression
	if !cc.Enabled {
		return
	}
	c.ContextCompression.Enabled = true
	c.ContextCompression.MaxTokens = cc.MaxTokens
	c.ContextCompression.ThresholdPercent = cc.ThresholdPercent
	c.ContextCompression.OnContextError = cc.OnContextError
	c.ContextCompression.PreserveRecentTurns = cc.PreserveRecentTurns
	if cc.Strategy != "" {
		c.ContextCompression.Strategy = cc.Strategy
	}
}

func (c *Config) mergeTelegram(other *Config) {
	if other.Telegram.Enabled {
		c.Telegram.Enabled = true
	}
}

func (c *Config) mergePermissions(other *Config) {
	if other.Permissions != nil {
		c.Permissions = other.Permissions
	}
}

// DeepCopy returns a deep copy of the Config.
func (c *Config) DeepCopy() *Config {
	out := &Config{}
	out.DeepMerge(c)
	return out
}
