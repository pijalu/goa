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
	"strings"

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
	Orchestrator       OrchestratorConfig       `yaml:"orchestrator,omitempty"`
	Goals              GoalsConfig              `yaml:"goals,omitempty"`
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
	MaxToolCalls             int                    `yaml:"max_tool_calls"`
	DisableToolBudget        bool                   `yaml:"disable_tool_budget"`
	ToolCallLimitResetWindow int                    `yaml:"tool_call_limit_reset_window"`
	MaxStreamRounds          int                    `yaml:"max_stream_rounds"`
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

// OrchestratorConfig configures the orchestrator subsystem: per-run topology
// selection, a config-only role→model map, a bounded agent pool with
// total + per-model caps, and retention settings. See docs/ORCHESTRATION-DESIGN.md.
type OrchestratorConfig struct {
	Roles     map[string]OrchestratorRole `yaml:"roles,omitempty"`
	Pool      OrchestratorPoolConfig      `yaml:"pool,omitempty"`
	Defaults  OrchestratorDefaultsConfig  `yaml:"defaults,omitempty"`
	Retention OrchestratorRetentionConfig `yaml:"retention,omitempty"`
}

// OrchestratorRetentionConfig controls how long finished orchestration runs
// are kept on disk. Enabled=false or Days=0 means "keep forever".
type OrchestratorRetentionConfig struct {
	Enabled bool `yaml:"enabled"`
	Days    int  `yaml:"days"`
}

// OrchestratorRole binds a role to a specific model/provider and tool allowlist.
type OrchestratorRole struct {
	Model        string   `yaml:"model"`
	Provider     string   `yaml:"provider,omitempty"`
	AllowedTools []string `yaml:"allowed_tools,omitempty"`
}

// OrchestratorPoolConfig bounds the live agent pool.
type OrchestratorPoolConfig struct {
	MaxTotalAgents  int            `yaml:"max_total_agents"`
	MaxAgentsPerModel map[string]int `yaml:"max_agents_per_model,omitempty"`
}

// OrchestratorDefaultsConfig holds default topology selection for new runs.
type OrchestratorDefaultsConfig struct {
	Topology        string `yaml:"topology"`
	RunTimeout      string `yaml:"run_timeout,omitempty"`      // per-run absolute wall-clock budget, e.g. "1h"; empty/invalid falls back to 1h
	ActivityTimeout string `yaml:"activity_timeout,omitempty"` // reset while events flow; empty/invalid falls back to 2m
}

// GoalsConfig controls the durable goal subsystem.
type GoalsConfig struct {
	Retention GoalsRetentionConfig `yaml:"retention,omitempty"`
}

// GoalsRetentionConfig controls how long terminal normal goals are kept.
// Enabled=false or Days=0 means "keep forever".
type GoalsRetentionConfig struct {
	Enabled bool `yaml:"enabled"`
	Days    int  `yaml:"days"`
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
	Bash        BashConfig           `yaml:"bash"`
	Terminal    TerminalConfig       `yaml:"terminal"`
	SSH         SSHConfig            `yaml:"ssh"`
	Search      SearchConfig         `yaml:"search"`
	SmartSearch SmartSearchConfig    `yaml:"smartsearch"`
	Edit        EditConfig           `yaml:"edit"`
	Python      PythonConfig         `yaml:"python"`
	ReadFile    tools.FileToolConfig `yaml:"read_file"`
	Write       WriteConfig          `yaml:"write"`
	WebFetch    tools.WebFetchConfig `yaml:"webfetch"`
	Enabled     ToolEnabledConfig    `yaml:"enabled"`
}

// SmartSearchConfig controls the smartsearch tool.
type SmartSearchConfig struct {
	Enabled     bool     `yaml:"enabled"`
	MaxResults  int      `yaml:"max_results"`
	MinScore    float64  `yaml:"min_score"`
	ExcludeDirs []string `yaml:"exclude_dirs"`
	K1          float64  `yaml:"k1"`
	B           float64  `yaml:"b"`
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
	Goal          bool `yaml:"goal"`
	Memento       bool `yaml:"memento"`
	PTYExec       bool `yaml:"pty_exec"`
	RequestReview bool `yaml:"request_review"`
	SSHBash       bool `yaml:"ssh_bash"`
	WebFetch      bool `yaml:"webfetch"`
	// Verify controls the `verify` tool (run the test suite). Opt-OUT: defaults
	// to true (set in the embedded default config) so the model can run tests
	// unless the user explicitly disables it.
	Verify bool `yaml:"verify"`
	// PythonEnabled controls the embedded `python` gpython tool. Opt-OUT:
	// defaults to true so the model can run Python code unless the user
	// explicitly disables it.
	PythonEnabled bool `yaml:"python"`
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
	if ptr := t.fieldPtr(name); ptr != nil {
		*ptr = value
	}
	if t.set == nil {
		t.set = make(map[string]bool)
	}
	t.set[name] = true
}

// GetEnabled returns the enabled flag for a known tool name, or false for
// unknown names.
func (t *ToolEnabledConfig) GetEnabled(name string) bool {
	if ptr := t.fieldPtr(name); ptr != nil {
		return *ptr
	}
	return false
}

// fieldPtr returns a pointer to the boolean field for the given tool name.
func (t *ToolEnabledConfig) fieldPtr(name string) *bool {
	fields := map[string]*bool{
		"bg_exec":          &t.BGExec,
		"delegate_to":      &t.DelegateTo,
		"goal":             &t.Goal,
		"memento":          &t.Memento,
		"pty_exec":         &t.PTYExec,
		"request_review":   &t.RequestReview,
		"ssh_bash":         &t.SSHBash,
		"webfetch":         &t.WebFetch,
		"verify":           &t.Verify,
		"python":           &t.PythonEnabled,
		"clarify_disabled": &t.ClarifyDisabled,
	}
	return fields[name]
}

// ApplyTo copies explicitly set flags from t into target.
func (t *ToolEnabledConfig) ApplyTo(target *ToolEnabledConfig) {
	if t.set == nil || target == nil {
		return
	}
	for name := range t.set {
		if srcPtr := t.fieldPtr(name); srcPtr != nil {
			if dstPtr := target.fieldPtr(name); dstPtr != nil {
				*dstPtr = *srcPtr
			}
		}
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
// Note: write does NOT support fuzzy filename matching — writing to the wrong
// path would cause irreversible data loss. The struct exists for future
// write-specific configuration options.
type WriteConfig struct{}

// PythonConfig controls the embedded gpython interpreter tool.
type PythonConfig struct {
	TimeoutSeconds int `yaml:"timeout_seconds"`
	// Jail confines the embedded interpreter's `os` file API to the project
	// directory and below, matching the bash tool's jail. When false, file
	// operations resolve against the project directory but absolute paths
	// outside it are permitted.
	Jail bool `yaml:"jail"`
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
	Jail           bool  `yaml:"jail"`
	// MaxOutputBytes caps the byte size of command output returned to the
	// agent. The tail of the output is kept. Zero defaults to 50KB.
	MaxOutputBytes int `yaml:"max_output_bytes"`
	// MaxComplexityScore caps the AST complexity score at which a shell command
	// is considered too complex for reliable static analysis. Zero defaults to
	// the analyzer's conservative threshold (50).
	MaxComplexityScore int `yaml:"max_complexity_score"`
	// EnableComplexityAnalysis enables the AST-based complexity analyzer.
	// When enabled, the LLM is told to keep bash scripts simple and avoid
	// dynamic command construction. Disabled by default.
	EnableComplexityAnalysis bool `yaml:"enable_complexity_analysis"`
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
	Tools          ToolDisplayConfig     `yaml:"tools"`
}

// ToolView is the default display mode for tool blocks in the chat.
type ToolView string

const (
	// ToolViewSummary shows a compact N-line preview of each tool call's
	// input/output (the default).
	ToolViewSummary ToolView = "summary"
	// ToolViewFull shows the complete input/output of every tool call.
	ToolViewFull ToolView = "full"
)

// ToolDisplayConfig controls how tool calls are rendered in the chat.
type ToolDisplayConfig struct {
	// View is the default mode: "summary" (collapsed, N-line preview) or
	// "full" (expanded). Defaults to "summary". Ctrl+O toggles all tool
	// blocks between the two modes for the running session.
	View ToolView `yaml:"view"`
	// PreviewLines is the number of input/output lines shown per tool block
	// in Summary mode. Defaults to 10. It is the single source of truth for
	// the collapsed line count across ALL tools (replaces the previous
	// inconsistent per-tool hardcodes).
	PreviewLines int `yaml:"preview_lines"`
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
//  1. Model-level compress_output (if set) → wins
//  2. Provider auto-detect (this function) → local = on, remote = off
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
	Level        string `yaml:"level"`
	File         string `yaml:"file"`
	TraceKeys    bool   `yaml:"trace_keys"`
	TerminalLog  string `yaml:"terminal_log"`
	RenderTrace  string `yaml:"render_trace"`
}

// Validate checks the config for semantic correctness.
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
// DeepCopy returns a deep copy of the Config.
func (c *Config) DeepCopy() *Config {
	out := &Config{}
	out.DeepMerge(c)
	return out
}
