// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"path/filepath"
	"testing"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/pijalu/goa/internal"
)

func TestMergeExecution_ToolCallLimitResetWindow(t *testing.T) {
	base := &Config{Execution: ExecutionConfig{MaxToolCalls: 10, ToolCallLimitResetWindow: 1}}
	override := &Config{Execution: ExecutionConfig{ToolCallLimitResetWindow: 5}}
	base.DeepMerge(override)
	if base.Execution.MaxToolCalls != 10 {
		t.Errorf("MaxToolCalls = %d, want 10", base.Execution.MaxToolCalls)
	}
	if base.Execution.ToolCallLimitResetWindow != 5 {
		t.Errorf("ToolCallLimitResetWindow = %d, want 5", base.Execution.ToolCallLimitResetWindow)
	}
}

// TestMergeExecution_DisableToolBudget verifies the DisableToolBudget field
// is merged correctly across config layers.
func TestMergeExecution_DisableToolBudget(t *testing.T) {
	base := &Config{Execution: ExecutionConfig{MaxToolCalls: 10}}
	override := &Config{Execution: ExecutionConfig{DisableToolBudget: true}}
	base.DeepMerge(override)
	if base.Execution.MaxToolCalls != 10 {
		t.Errorf("MaxToolCalls = %d, want 10", base.Execution.MaxToolCalls)
	}
	if !base.Execution.DisableToolBudget {
		t.Error("DisableToolBudget should be true after merge")
	}
}

// TestMergeExecution_LoopDetectionDisable verifies the tri-state loop
// detection disable fields merge correctly: nil leaves the base untouched,
// an explicit value overrides (including false-over-true for the cascade).
func TestMergeExecution_LoopDetectionDisable(t *testing.T) {
	trueVal, falseVal := true, false

	// nil override leaves base untouched.
	base := &Config{Execution: ExecutionConfig{DisableThinkingLoopDetection: &trueVal}}
	base.DeepMerge(&Config{})
	if base.Execution.DisableThinkingLoopDetection == nil || !*base.Execution.DisableThinkingLoopDetection {
		t.Error("nil override should not clear DisableThinkingLoopDetection")
	}

	// Explicit false in a later layer overrides true (cascade: project re-enables
	// what home disabled).
	base.DeepMerge(&Config{Execution: ExecutionConfig{DisableThinkingLoopDetection: &falseVal}})
	if base.Execution.DisableThinkingLoopDetection == nil || *base.Execution.DisableThinkingLoopDetection {
		t.Error("explicit false should override true")
	}

	// Tool detection merges independently.
	base.DeepMerge(&Config{Execution: ExecutionConfig{DisableToolLoopDetection: &trueVal}})
	if base.Execution.DisableToolLoopDetection == nil || !*base.Execution.DisableToolLoopDetection {
		t.Error("DisableToolLoopDetection should be true after merge")
	}
}

func assertContextCompression(t *testing.T, cfg Config) {
	t.Helper()
	if !cfg.ContextCompression.EnabledValue() {
		t.Error("ContextCompression.EnabledValue() should be true")
	}
	if cfg.ContextCompression.MaxTokens != 8192 {
		t.Errorf("ContextCompression.MaxTokens = %d, want 8192", cfg.ContextCompression.MaxTokens)
	}
	if cfg.ContextCompression.ThresholdPercent != 80 {
		t.Errorf("ContextCompression.ThresholdPercent = %d, want 80", cfg.ContextCompression.ThresholdPercent)
	}
}

func TestConfigValidate_AgenticFields(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name: "valid agentic fields",
			cfg: &Config{
				Execution: ExecutionConfig{Mode: internal.ExecutionYolo, WorktreeMode: internal.WorktreeAlways},
				Providers: []ProviderConfig{{
					ID:             "openai",
					Provider:       AgenticProviderOpenAI,
					API:            AgenticAPIOpenAICompletions,
					Transport:      AgenticTransportSSE,
					CacheRetention: AgenticCacheRetentionShort,
					MaxRetryDelay:  "2s",
				}},
				Skills: SkillsConfig{ExecutionMode: AgenticSkillModeInline},
				ContextCompression: ContextCompressionConfig{
					Enabled:          boolPtr(true),
					Strategy:         AgenticCompressionToolElision,
					ThresholdPercent: 75,
				},
			},
			wantErr: false,
		},
		{
			name: "unknown provider",
			cfg: &Config{
				Execution: ExecutionConfig{Mode: internal.ExecutionYolo, WorktreeMode: internal.WorktreeAlways},
				Providers: []ProviderConfig{{ID: "x", Provider: "unknown"}},
			},
			wantErr: true,
		},
		{
			name: "unknown api",
			cfg: &Config{
				Execution: ExecutionConfig{Mode: internal.ExecutionYolo, WorktreeMode: internal.WorktreeAlways},
				Providers: []ProviderConfig{{ID: "x", API: "unknown"}},
			},
			wantErr: true,
		},
		{
			name: "invalid transport",
			cfg: &Config{
				Execution: ExecutionConfig{Mode: internal.ExecutionYolo, WorktreeMode: internal.WorktreeAlways},
				Providers: []ProviderConfig{{ID: "x", Transport: "grpc"}},
			},
			wantErr: true,
		},
		{
			name: "invalid cache_retention",
			cfg: &Config{
				Execution: ExecutionConfig{Mode: internal.ExecutionYolo, WorktreeMode: internal.WorktreeAlways},
				Providers: []ProviderConfig{{ID: "x", CacheRetention: "forever"}},
			},
			wantErr: true,
		},
		{
			name: "invalid skill mode",
			cfg: &Config{
				Execution: ExecutionConfig{Mode: internal.ExecutionYolo, WorktreeMode: internal.WorktreeAlways},
				Skills:    SkillsConfig{ExecutionMode: "agent"},
			},
			wantErr: true,
		},
		{
			name: "invalid compression strategy",
			cfg: &Config{
				Execution: ExecutionConfig{Mode: internal.ExecutionYolo, WorktreeMode: internal.WorktreeAlways},
				ContextCompression: ContextCompressionConfig{
					Enabled:  boolPtr(true),
					Strategy: "unknown",
				},
			},
			wantErr: true,
		},
		{
			name: "invalid threshold percent",
			cfg: &Config{
				Execution: ExecutionConfig{Mode: internal.ExecutionYolo, WorktreeMode: internal.WorktreeAlways},
				ContextCompression: ContextCompressionConfig{
					Enabled:          boolPtr(true),
					ThresholdPercent: 150,
				},
			},
			wantErr: true,
		},
		{
			name: "valid retry_policy always",
			cfg: &Config{
				Providers: []ProviderConfig{{
					ID: "x", Provider: AgenticProviderOpenAI,
					RetryPolicy: &RetryPolicyConfig{Mode: "always", MaxRetries: 7,
						Backoff: RetryBackoffConfig{InitialMS: 500, MaxMS: 5000, Jitter: 0.2},
						Codes:   []string{"RATE_LIMIT", "SERVER"}},
				}},
			},
			wantErr: false,
		},
		{
			name: "valid retry_policy normal empty codes",
			cfg: &Config{
				Providers: []ProviderConfig{{
					ID: "x", Provider: AgenticProviderOpenAI,
					RetryPolicy: &RetryPolicyConfig{Mode: "normal", MaxRetries: 1},
				}},
			},
			wantErr: false,
		},
		{
			name: "invalid retry_policy mode",
			cfg: &Config{
				Providers: []ProviderConfig{{
					ID: "x", Provider: AgenticProviderOpenAI,
					RetryPolicy: &RetryPolicyConfig{Mode: "sometimes"},
				}},
			},
			wantErr: true,
		},
		{
			name: "invalid retry_policy negative max_retries",
			cfg: &Config{
				Providers: []ProviderConfig{{
					ID: "x", Provider: AgenticProviderOpenAI,
					RetryPolicy: &RetryPolicyConfig{Mode: "normal", MaxRetries: -1},
				}},
			},
			wantErr: true,
		},
		{
			name: "invalid retry_policy backoff initial > max",
			cfg: &Config{
				Providers: []ProviderConfig{{
					ID: "x", Provider: AgenticProviderOpenAI,
					RetryPolicy: &RetryPolicyConfig{Mode: "normal",
						Backoff: RetryBackoffConfig{InitialMS: 5000, MaxMS: 1000}},
				}},
			},
			wantErr: true,
		},
		{
			name: "invalid retry_policy jitter out of range",
			cfg: &Config{
				Providers: []ProviderConfig{{
					ID: "x", Provider: AgenticProviderOpenAI,
					RetryPolicy: &RetryPolicyConfig{Mode: "normal",
						Backoff: RetryBackoffConfig{Jitter: 1.5}},
				}},
			},
			wantErr: true,
		},
		{
			name: "invalid retry_policy duplicate codes",
			cfg: &Config{
				Providers: []ProviderConfig{{
					ID: "x", Provider: AgenticProviderOpenAI,
					RetryPolicy: &RetryPolicyConfig{Mode: "normal", Codes: []string{"SERVER", "SERVER"}},
				}},
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			gotErr := err != nil
			if gotErr != tt.wantErr {
				t.Errorf("Validate() err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

func TestConfig_GetReasoningEffort(t *testing.T) {
	cfg := &Config{ThinkingLevels: ThinkingLevelConfig{Default: "medium", MainAgent: "high"}}
	if got := cfg.GetReasoningEffort(); got != "high" {
		t.Errorf("GetReasoningEffort() = %q, want high", got)
	}
}

// TestConfig_ValidateTimeContext verifies the time_context validation rules:
// a malformed refresh interval and an unsupported IANA zone are rejected,
// while the disabled default and valid settings pass.
func TestConfig_ValidateTimeContext(t *testing.T) {
	tests := []struct {
		name    string
		cfg     *Config
		wantErr bool
	}{
		{
			name: "disabled default passes",
			cfg:  &Config{TimeContext: TimeContextConfig{}},
		},
		{
			name: "enabled with valid zone and interval passes",
			cfg: &Config{TimeContext: TimeContextConfig{
				Enabled:         true,
				TimeZone:        "Asia/Shanghai",
				RefreshInterval: "60s",
			}},
		},
		{
			name: "invalid refresh interval rejected",
			cfg: &Config{TimeContext: TimeContextConfig{
				Enabled:         true,
				RefreshInterval: "not-a-duration",
			}},
			wantErr: true,
		},
		{
			name: "unsupported time zone rejected",
			cfg: &Config{TimeContext: TimeContextConfig{
				Enabled:  true,
				TimeZone: "Not/AZone",
			}},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.cfg.Validate()
			gotErr := err != nil
			if gotErr != tt.wantErr {
				t.Errorf("Validate() err=%v, wantErr=%v", err, tt.wantErr)
			}
		})
	}
}

// TestConfig_MergeTimeContext verifies the cascade merge semantics (CX6):
// the enable switch propagates upward (default off), and zone/interval
// propagate when set.
func TestConfig_MergeTimeContext(t *testing.T) {
	base := &Config{}
	over := &Config{TimeContext: TimeContextConfig{
		Enabled:         true,
		TimeZone:        "Asia/Shanghai",
		RefreshInterval: "5m",
	}}
	base.DeepMerge(over)
	if !base.TimeContext.Enabled {
		t.Error("DeepMerge must propagate Enabled=true")
	}
	if base.TimeContext.TimeZone != "Asia/Shanghai" {
		t.Errorf("DeepMerge TimeZone = %q, want Asia/Shanghai", base.TimeContext.TimeZone)
	}
	if base.TimeContext.RefreshInterval != "5m" {
		t.Errorf("DeepMerge RefreshInterval = %q, want 5m", base.TimeContext.RefreshInterval)
	}
}

func TestConfig_GetToolResultAsUser(t *testing.T) {
	trueVal := true
	cfg := &Config{
		Providers: []ProviderConfig{{ID: "x", ToolResultAsUser: &trueVal}},
	}
	cfg.ActiveProvider = "x"
	if got := cfg.GetToolResultAsUser(); got == nil || !*got {
		t.Error("GetToolResultAsUser() should return true")
	}

	cfg2 := &Config{Providers: []ProviderConfig{{ID: "y"}}}
	cfg2.ActiveProvider = "y"
	if got := cfg2.GetToolResultAsUser(); got != nil {
		t.Error("GetToolResultAsUser() should return nil when not set")
	}
}

func TestConfig_GetActiveProviderConfig(t *testing.T) {
	cfg := &Config{
		ActiveProvider: "x",
		Providers:      []ProviderConfig{{ID: "x"}, {ID: "y", Preferred: true}},
	}
	if got := cfg.GetActiveProviderConfig(); got == nil || got.ID != "x" {
		t.Errorf("GetActiveProviderConfig() = %v, want x", got)
	}

	cfg.ActiveProvider = "missing"
	if got := cfg.GetActiveProviderConfig(); got == nil || got.ID != "y" {
		t.Errorf("GetActiveProviderConfig() fallback = %v, want y", got)
	}
}

func TestConfig_GetActiveModelConfig(t *testing.T) {
	cfg := &Config{
		ActiveProvider: "x",
		ActiveModel:    "m1",
		Providers:      []ProviderConfig{{ID: "x"}},
		Models:         []ModelConfig{{ID: "m1", ProviderID: "x", Model: "gpt-4o"}},
	}
	got, err := cfg.GetActiveModelConfig()
	if err != nil {
		t.Fatalf("GetActiveModelConfig() unexpected error: %v", err)
	}
	if got.ID != "m1" || got.Model != "gpt-4o" {
		t.Errorf("GetActiveModelConfig() = %+v, want m1/gpt-4o", got)
	}

	cfg.ActiveModel = ""
	got, err = cfg.GetActiveModelConfig()
	if err != nil {
		t.Fatalf("GetActiveModelConfig() fallback error: %v", err)
	}
	if got.ID != "m1" {
		t.Errorf("GetActiveModelConfig() fallback = %+v, want m1", got)
	}

	// Ambiguous: multiple models match the active provider without
	// active_model — resolution is deterministic: first in config order.
	cfg.Models = append(cfg.Models, ModelConfig{ID: "m2", ProviderID: "x", Model: "gpt-4o-mini"})
	got, err = cfg.GetActiveModelConfig()
	if err != nil {
		t.Fatalf("GetActiveModelConfig() ambiguous error: %v", err)
	}
	if got.ID != "m1" {
		t.Errorf("GetActiveModelConfig() ambiguous = %q, want first match %q", got.ID, "m1")
	}
}

func TestConfig_GetActiveModelConfig_Errors(t *testing.T) {
	// Missing active model with no providers
	cfg := &Config{}
	if _, err := cfg.GetActiveModelConfig(); err == nil {
		t.Error("expected error when no provider is configured")
	}

	// Explicit active_model not found
	cfg = &Config{ActiveModel: "missing", Providers: []ProviderConfig{{ID: "x"}}}
	if _, err := cfg.GetActiveModelConfig(); err == nil {
		t.Error("expected error when active_model is not found")
	}

	// No model for provider
	cfg = &Config{
		ActiveProvider: "x",
		Providers:      []ProviderConfig{{ID: "x"}},
	}
	if _, err := cfg.GetActiveModelConfig(); err == nil {
		t.Error("expected error when no model matches provider")
	}
}

func TestDeepMerge_ContextCompression(t *testing.T) {
	base := &Config{}
	override := &Config{
		ContextCompression: ContextCompressionConfig{
			Enabled:             boolPtr(true),
			MaxTokens:           4096,
			ThresholdPercent:    75,
			OnContextError:      true,
			Strategy:            AgenticCompressionSelective,
			PreserveRecentTurns: 4,
		},
	}
	base.DeepMerge(override)
	if !base.ContextCompression.EnabledValue() {
		t.Error("ContextCompression.EnabledValue() should be true")
	}
	if base.ContextCompression.MaxTokens != 4096 {
		t.Errorf("ContextCompression.MaxTokens = %d, want 4096", base.ContextCompression.MaxTokens)
	}
	if base.ContextCompression.PreserveRecentTurns != 4 {
		t.Errorf("ContextCompression.PreserveRecentTurns = %d, want 4", base.ContextCompression.PreserveRecentTurns)
	}
}

func TestDeepMerge_SkillsExecutionMode(t *testing.T) {
	base := &Config{Skills: SkillsConfig{ExecutionMode: AgenticSkillModeSubAgent}}
	override := &Config{Skills: SkillsConfig{ExecutionMode: AgenticSkillModeInline}}
	base.DeepMerge(override)
	if base.Skills.ExecutionMode != AgenticSkillModeInline {
		t.Errorf("Skills.ExecutionMode = %q, want %q", base.Skills.ExecutionMode, AgenticSkillModeInline)
	}
}

func TestToolEnabledConfigDefaults(t *testing.T) {
	cfg := &Config{}
	if cfg.Tools.Enabled.BGExec {
		t.Error("BGExec should be disabled by default")
	}
	if cfg.Tools.Enabled.Terminals {
		t.Error("Terminals should be disabled by default")
	}
	if cfg.Tools.Enabled.SSHBash {
		t.Error("SSHBash should be disabled by default")
	}
	if cfg.Tools.Enabled.Memento {
		t.Error("Memento should be disabled by default")
	}
	if cfg.Tools.Enabled.DelegateTo {
		t.Error("DelegateTo should be disabled in a zero Config (embedded default.yaml enables it)")
	}
	if cfg.Tools.Enabled.RequestReview {
		t.Error("RequestReview should be disabled in a zero Config (embedded default.yaml enables it)")
	}
	if cfg.Tools.Enabled.Goal {
		t.Error("Goal should be disabled by default")
	}
}

// TestAgentToolsConfigurable verifies the sub-agent/swarm/goa tools are
// toggleable through ToolEnabledConfig like every other configurable tool.
func TestAgentToolsConfigurable(t *testing.T) {
	for _, name := range []string{"agent", "agent_swarm", "goa"} {
		cfg := ToolEnabledConfig{}
		if cfg.GetEnabled(name) {
			t.Errorf("%s should default to false on a zero-value config", name)
		}
		cfg.SetEnabled(name, true)
		if !cfg.GetEnabled(name) {
			t.Errorf("GetEnabled(%s) should return true after SetEnabled", name)
		}
		cfg.SetEnabled(name, false)
		if cfg.GetEnabled(name) {
			t.Errorf("GetEnabled(%s) should return false after disable", name)
		}
	}
}

// TestAgentToolsYAMLRoundTrip verifies the new keys parse from YAML.
func TestAgentToolsYAMLRoundTrip(t *testing.T) {
	y := `
tools:
  enabled:
    agent: false
    agent_swarm: false
    goa: false
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(y), &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if cfg.Tools.Enabled.Agent {
		t.Error("Agent should be false")
	}
	if cfg.Tools.Enabled.AgentSwarm {
		t.Error("AgentSwarm should be false")
	}
	if cfg.Tools.Enabled.Goa {
		t.Error("Goa should be false")
	}
}

// TestAgentToolsDefaultEnabled verifies the embedded default config keeps
// only the lean core tool set on by default (terminals, python, webfetch) and
// ships the heavier agent-driven/sub-agent tools off — the tuned default
// adopted from the maintainer config. Users opt in per tool via /tools.
func TestAgentToolsDefaultEnabled(t *testing.T) {
	yamlText, err := DefaultConfigYAML()
	if err != nil {
		t.Fatalf("load embedded default: %v", err)
	}
	var cfg Config
	if err := yaml.Unmarshal([]byte(yamlText), &cfg); err != nil {
		t.Fatalf("Unmarshal embedded default failed: %v", err)
	}
	for _, name := range []string{"terminals", "python", "webfetch"} {
		if !cfg.Tools.Enabled.GetEnabled(name) {
			t.Errorf("embedded default should enable %s", name)
		}
	}
	for _, name := range []string{"agent", "agent_swarm", "goa", "lsp", "todo_list", "verify", "delegate_to", "request_review", "bg_exec"} {
		if cfg.Tools.Enabled.GetEnabled(name) {
			t.Errorf("embedded default should disable %s (opt-in)", name)
		}
	}
}

func TestToolEnabledConfigRoundTrip(t *testing.T) {
	y := `
tools:
  enabled:
    bg_exec: true
    terminals: true
    memento: true
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(y), &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if !cfg.Tools.Enabled.BGExec {
		t.Error("BGExec should be true")
	}
	if !cfg.Tools.Enabled.Terminals {
		t.Error("Terminals should be true")
	}
	if !cfg.Tools.Enabled.Memento {
		t.Error("Memento should be true")
	}
	if cfg.Tools.Enabled.SSHBash {
		t.Error("SSHBash should be false")
	}
}

func TestToolEnabledConfigMergeOverridesOnlySetKeys(t *testing.T) {
	base := &Config{}
	base.Tools.Enabled.BGExec = true
	base.Tools.Enabled.SetEnabled("bg_exec", true)

	override := &Config{}
	override.Tools.Enabled.SetEnabled("bg_exec", false)

	base.DeepMerge(override)
	if base.Tools.Enabled.BGExec {
		t.Error("BGExec should be overridden to false")
	}
	// Memento was not set in override, so base default (false) should remain.
	if base.Tools.Enabled.Memento {
		t.Error("Memento should remain false")
	}
}

func TestToolEnabledConfigSetEnabled(t *testing.T) {
	cfg := ToolEnabledConfig{}
	cfg.SetEnabled("memento", true)
	if !cfg.Memento {
		t.Error("Memento should be true")
	}
	cfg.SetEnabled("goal", true)
	if !cfg.Goal {
		t.Error("Goal should be true")
	}
	if cfg.BGExec {
		t.Error("BGExec should remain false")
	}
}

// TestClarifyDefaultEnabled verifies the clarify tool is enabled by default
// (ClarifyDisabled == false), unlike every other flag which is opt-IN.
func TestClarifyDefaultEnabled(t *testing.T) {
	cfg := &Config{}
	if cfg.Tools.Enabled.ClarifyDisabled {
		t.Error("ClarifyDisabled should be false by default (tool enabled by default)")
	}
}

// TestClarifyDisabledRoundTrip verifies the clarify_disabled YAML key parses
// and round-trips through the inverted flag.
func TestClarifyDisabledRoundTrip(t *testing.T) {
	y := `
tools:
  enabled:
    clarify_disabled: true
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(y), &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if !cfg.Tools.Enabled.ClarifyDisabled {
		t.Error("ClarifyDisabled should be true after parsing clarify_disabled: true")
	}
	if !cfg.Tools.Enabled.set["clarify_disabled"] {
		t.Error("clarify_disabled key should be recorded as explicitly set")
	}
}

// TestClarifyDisabledDeepMerge verifies the inverted flag survives a deep
// merge only when explicitly set, without clobbering unrelated flags.
func TestClarifyDisabledDeepMerge(t *testing.T) {
	base := &Config{}
	base.Tools.Enabled.SetEnabled("memento", true) // unrelated opt-in flag

	override := &Config{}
	override.Tools.Enabled.SetEnabled("clarify_disabled", true)

	base.DeepMerge(override)
	if !base.Tools.Enabled.ClarifyDisabled {
		t.Error("ClarifyDisabled should be overridden to true")
	}
	if !base.Tools.Enabled.Memento {
		t.Error("Memento should remain true (not clobbered by clarify merge)")
	}
}

func TestResolvePlanFilePath_DefaultUsesProjectDir(t *testing.T) {
	base := t.TempDir()
	cfg := Config{}
	got := cfg.ResolvePlanFilePath(base)
	want := filepath.Join(base, ".goa", "plan.md")
	if got != want {
		t.Errorf("ResolvePlanFilePath = %q, want %q", got, want)
	}
}

func TestResolvePlanFilePath_ExplicitPath(t *testing.T) {
	base := t.TempDir()
	cfg := Config{Mode: ModeConfig{PlanFilePath: "plans/my-plan.md"}}
	got := cfg.ResolvePlanFilePath(base)
	want := filepath.Join(base, "plans", "my-plan.md")
	if got != want {
		t.Errorf("ResolvePlanFilePath = %q, want %q", got, want)
	}
}

func TestResolvePlanFilePath_AbsolutePath(t *testing.T) {
	abs := filepath.Join(t.TempDir(), "plans", "plan.md")
	cfg := Config{Mode: ModeConfig{PlanFilePath: abs}}
	got := cfg.ResolvePlanFilePath("/should/be/ignored")
	want := abs
	if got != want {
		t.Errorf("ResolvePlanFilePath = %q, want %q", got, want)
	}
}

// TestToolEnabledConfigPython verifies python flag getters and setters.
func TestToolEnabledConfigPython(t *testing.T) {
	cfg := ToolEnabledConfig{}
	if cfg.GetEnabled("python") {
		t.Error("PythonEnabled should be false by default")
	}
	cfg.SetEnabled("python", true)
	if !cfg.PythonEnabled {
		t.Error("PythonEnabled should be true after SetEnabled")
	}
	if !cfg.GetEnabled("python") {
		t.Error("GetEnabled(python) should return true")
	}
	if cfg.GetEnabled("unknown_tool") {
		t.Error("GetEnabled for unknown tool should return false")
	}
	if cfg.set == nil || !cfg.set["python"] {
		t.Error("SetEnabled should mark python as explicitly set")
	}
}

// TestToolEnabledConfigUnknownSetEnabled verifies unknown tool names are
// recorded but do not crash.
func TestToolEnabledConfigUnknownSetEnabled(t *testing.T) {
	cfg := ToolEnabledConfig{}
	cfg.SetEnabled("unknown_tool", true)
	if cfg.GetEnabled("unknown_tool") {
		t.Error("GetEnabled for unknown tool should return false")
	}
	if cfg.set == nil || !cfg.set["unknown_tool"] {
		t.Error("SetEnabled should mark unknown tool as explicitly set")
	}
}

// TestToolEnabledConfigPythonApplyTo verifies python flag is copied by ApplyTo.
func TestToolEnabledConfigPythonApplyTo(t *testing.T) {
	src := ToolEnabledConfig{}
	src.SetEnabled("python", true)

	dst := ToolEnabledConfig{}
	src.ApplyTo(&dst)
	if !dst.PythonEnabled {
		t.Error("PythonEnabled should be copied by ApplyTo")
	}
	if dst.set == nil || !dst.set["python"] {
		t.Error("ApplyTo should mark python as explicitly set in target")
	}
}

// TestGoalsAutoUnblockEnabled verifies the tri-state default: nil = on,
// explicit false = off, and a higher-layer explicit value overrides the
// embedded default true in a DeepMerge.
func TestGoalsAutoUnblockEnabled(t *testing.T) {
	// Default (nil) is enabled.
	var def Config
	if !def.Goals.AutoUnblockEnabled() {
		t.Error("AutoUnblockEnabled() = false for nil, want default true")
	}
	// Explicit false disables.
	off := false
	c := Config{Goals: GoalsConfig{AutoUnblock: &off}}
	if c.Goals.AutoUnblockEnabled() {
		t.Error("AutoUnblockEnabled() = true for explicit false, want false")
	}
	// Explicit false in the override layer wins over default true in base.
	base := Config{Goals: GoalsConfig{AutoUnblock: boolPtr(true)}}
	override := Config{Goals: GoalsConfig{AutoUnblock: &off}}
	base.DeepMerge(&override)
	if base.Goals.AutoUnblockEnabled() {
		t.Error("DeepMerge: explicit false override should disable auto_unblock")
	}
}

// TestGoalsFreshContextEnabled verifies the tri-state fresh-context default
// (Issue 24): nil = on (clean context per goal), explicit false = reuse
// conversation, and a higher-layer explicit value overrides in a DeepMerge.
func TestGoalsFreshContextEnabled(t *testing.T) {
	var def Config
	if !def.Goals.FreshContextEnabled() {
		t.Error("FreshContextEnabled() = false for nil, want default true")
	}
	off := false
	c := Config{Goals: GoalsConfig{FreshContext: &off}}
	if c.Goals.FreshContextEnabled() {
		t.Error("FreshContextEnabled() = true for explicit false, want false")
	}
	base := Config{Goals: GoalsConfig{FreshContext: boolPtr(true)}}
	override := Config{Goals: GoalsConfig{FreshContext: &off}}
	base.DeepMerge(&override)
	if base.Goals.FreshContextEnabled() {
		t.Error("DeepMerge: explicit false override should disable fresh_context")
	}
}

// TestGoalsVerifyTimeoutOr verifies goals.verify_timeout parsing
// Bug A): empty/invalid falls back to the default; valid durations win.
func TestGoalsVerifyTimeoutOr(t *testing.T) {
	fallback := 2 * time.Minute
	var def Config
	if got := def.Goals.VerifyTimeoutOr(fallback); got != fallback {
		t.Errorf("empty verify_timeout = %v, want fallback %v", got, fallback)
	}
	c := Config{Goals: GoalsConfig{VerifyTimeout: "90s"}}
	if got := c.Goals.VerifyTimeoutOr(fallback); got != 90*time.Second {
		t.Errorf("verify_timeout 90s = %v, want 1m30s", got)
	}
	bad := Config{Goals: GoalsConfig{VerifyTimeout: "soon"}}
	if got := bad.Goals.VerifyTimeoutOr(fallback); got != fallback {
		t.Errorf("invalid verify_timeout = %v, want fallback %v", got, fallback)
	}
}

func boolPtr(b bool) *bool { return &b }

// TestGetThinkingLevel_MainAgentPrefersModelLevel verifies the main-agent
// thinking-level resolution order: the active model's own thinking_level wins
// over the global thinking_levels.main_agent, which wins over the default.
// Per-model values are where runtime thinking-level changes are saved, so
// they must shadow the global default for each model to keep its own level.
func TestGetThinkingLevel_MainAgentPrefersModelLevel(t *testing.T) {
	newCfg := func() *Config {
		return &Config{
			ActiveProvider: "p1",
			Providers:      []ProviderConfig{{ID: "p1"}},
			ActiveModel:    "deepseek",
			Models: []ModelConfig{
				{ID: "deepseek", ProviderID: "p1", ThinkingLevel: "high"},
				{ID: "kimi", ProviderID: "p1", ThinkingLevel: "low"},
			},
			ThinkingLevels: ThinkingLevelConfig{MainAgent: "medium", Default: "minimal"},
		}
	}

	tests := []struct {
		name        string
		mutate      func(cfg *Config)
		activeModel string
		want        internal.ThinkingLevel
	}{
		{
			name:        "model level wins over main_agent",
			activeModel: "deepseek",
			want:        internal.ThinkingLevelHigh,
		},
		{
			name:        "other model keeps its own level",
			activeModel: "kimi",
			want:        internal.ThinkingLevelLow,
		},
		{
			name: "model without level falls back to main_agent",
			mutate: func(cfg *Config) {
				cfg.GetModelByID("deepseek").ThinkingLevel = ""
			},
			activeModel: "deepseek",
			want:        internal.ThinkingLevelMedium,
		},
		{
			name: "no model level and no main_agent falls back to default",
			mutate: func(cfg *Config) {
				cfg.GetModelByID("deepseek").ThinkingLevel = ""
				cfg.ThinkingLevels.MainAgent = ""
			},
			activeModel: "deepseek",
			want:        internal.ThinkingLevelMinimal,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := newCfg()
			if tt.mutate != nil {
				tt.mutate(cfg)
			}
			cfg.ActiveModel = tt.activeModel
			if got := cfg.GetThinkingLevel("main_agent"); got != tt.want {
				t.Errorf("GetThinkingLevel(main_agent) = %q, want %q", got, tt.want)
			}
		})
	}
}
