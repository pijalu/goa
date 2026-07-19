// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"testing"

	"github.com/pijalu/goa/internal"
	"gopkg.in/yaml.v3"
)

// TestConfigDeserializeFromYAML verifies Config struct deserializes from YAML.
func TestConfigDeserializeFromYAML(t *testing.T) {
	y := `
active_provider: openai
active_model: gpt-4o
active_profile: coder
execution:
  mode: yolo
  retries: 3
  token_warning: 70
  token_critical: 90
  loop_warning: 3
  loop_interrupt: 5
  activity_timeout: 30s
  error_threshold: 0.5
  worktree_mode: always
  auto_save_model: true
providers:
  - id: openai
    name: OpenAI
    endpoint: https://api.openai.com/v1
    api_key: sk-test
    default_model: gpt-4o
    timeout: 60s
    max_retries: 3
    preferred: true
tui:
  theme: dark
  layout: default
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(y), &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if cfg.ActiveProvider != "openai" {
		t.Errorf("ActiveProvider = %q, want %q", cfg.ActiveProvider, "openai")
	}
	if cfg.ActiveModel != "gpt-4o" {
		t.Errorf("ActiveModel = %q, want %q", cfg.ActiveModel, "gpt-4o")
	}
	// active_profile is no longer a Config field; it is migrated by the loader
	if cfg.Mode.Default.Major != "" {
		t.Errorf("Mode.Default.Major = %q, want empty", cfg.Mode.Default.Major)
	}
	if cfg.Execution.Mode != internal.ExecutionYolo {
		t.Errorf("Mode = %q, want %q", cfg.Execution.Mode, internal.ExecutionYolo)
	}
	if cfg.Execution.Retries != 3 {
		t.Errorf("Retries = %d, want 3", cfg.Execution.Retries)
	}
	if cfg.Execution.WorktreeMode != internal.WorktreeAlways {
		t.Errorf("WorktreeMode = %q, want %q", cfg.Execution.WorktreeMode, internal.WorktreeAlways)
	}
	if len(cfg.Providers) != 1 {
		t.Fatalf("Providers = %d, want 1", len(cfg.Providers))
	}
	if cfg.Providers[0].ID != "openai" {
		t.Errorf("Provider ID = %q, want %q", cfg.Providers[0].ID, "openai")
	}
	if cfg.TUI.Theme != "dark" {
		t.Errorf("TUI Theme = %q, want %q", cfg.TUI.Theme, "dark")
	}
}

func TestConfigModelPricingRoundTrip(t *testing.T) {
	y := `
models:
  - id: gpt4o
    model: gpt-4o
    provider: openai
    pricing:
      input_per_1m: 2.50
      output_per_1m: 10.00
      cache_read_per_1m: 1.25
      cache_write_per_1m: 3.75
    cache:
      enabled: true
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(y), &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if len(cfg.Models) != 1 {
		t.Fatalf("Models = %d, want 1", len(cfg.Models))
	}
	m := cfg.Models[0]
	if m.ID != "gpt4o" {
		t.Errorf("Model ID = %q, want gpt4o", m.ID)
	}
	if m.Pricing == nil {
		t.Fatal("Pricing is nil")
	}
	if m.Pricing.InputPer1M != 2.50 {
		t.Errorf("InputPer1M = %f, want 2.50", m.Pricing.InputPer1M)
	}
	if m.Pricing.OutputPer1M != 10.00 {
		t.Errorf("OutputPer1M = %f, want 10.00", m.Pricing.OutputPer1M)
	}
	if m.Cache == nil || !m.Cache.Enabled {
		t.Errorf("Cache.Enabled = false, want true")
	}
}

func TestConfigModelPricingDefaults(t *testing.T) {
	y := `
models:
  - id: default
    model: default-model
    provider: local
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(y), &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	m := cfg.Models[0]
	if m.Pricing != nil {
		t.Errorf("Pricing = %+v, want nil (zero-pricing default)", m.Pricing)
	}
	if m.Cache != nil {
		t.Errorf("Cache = %+v, want nil (cache disabled by default)", m.Cache)
	}
}

// TestConfigUnknownKeysIgnored verifies forward-compat: unknown keys don't error.
func TestConfigUnknownKeysIgnored(t *testing.T) {
	y := `
active_provider: test
unknown_key: should_not_error
nested_unknown:
  inner: value
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(y), &cfg); err != nil {
		t.Errorf("Unknown keys should not cause error: %v", err)
	}
}

// TestConfigValidateMode verifies mode validation.
func TestConfigValidateMode(t *testing.T) {
	tests := []struct {
		mode    internal.ExecutionMode
		wantErr bool
	}{
		{internal.ExecutionYolo, false},
		{internal.ExecutionConfirm, false},
		{internal.ExecutionReview, false},
		{"invalid", true},
		{"", false}, // empty is allowed (default)
	}
	for _, tt := range tests {
		cfg := &Config{Execution: ExecutionConfig{Mode: tt.mode}}
		// Add required fields to avoid spurious errors
		cfg.Execution.WorktreeMode = internal.WorktreeAlways
		err := cfg.Validate()
		gotErr := err != nil
		if gotErr != tt.wantErr {
			t.Errorf("Validate(mode=%q) err=%v, wantErr=%v", tt.mode, err, tt.wantErr)
		}
	}
}

// TestConfigValidateWorktreeMode verifies worktree mode validation.
func TestConfigValidateWorktreeMode(t *testing.T) {
	tests := []struct {
		mode    internal.WorktreeMode
		wantErr bool
	}{
		{internal.WorktreeAlways, false},
		{internal.WorktreeMultiAgent, false},
		{"invalid", true},
		{"", false},
	}
	for _, tt := range tests {
		cfg := &Config{Execution: ExecutionConfig{Mode: internal.ExecutionYolo, WorktreeMode: tt.mode}}
		err := cfg.Validate()
		gotErr := err != nil
		if gotErr != tt.wantErr {
			t.Errorf("Validate(worktree=%q) err=%v, wantErr=%v", tt.mode, err, tt.wantErr)
		}
	}
}

// TestConfigValidateActiveProvider verifies provider existence check.
func TestConfigValidateActiveProvider(t *testing.T) {
	cfg := &Config{
		ActiveProvider: "nonexistent",
		Execution:      ExecutionConfig{Mode: internal.ExecutionYolo, WorktreeMode: internal.WorktreeAlways},
		Providers:      []ProviderConfig{{ID: "openai"}},
	}
	err := cfg.Validate()
	if err == nil {
		t.Error("Expected error for nonexistent active_provider")
	}
}

// TestConfigValidateTimeout verifies duration parsing.
func TestConfigValidateTimeout(t *testing.T) {
	tests := []struct {
		timeout string
		wantErr bool
	}{
		{"30s", false},
		{"5m", false},
		{"1h", false},
		{"invalid", true},
		{"", false},
	}
	for _, tt := range tests {
		cfg := &Config{
			Execution: ExecutionConfig{
				Mode:            internal.ExecutionYolo,
				WorktreeMode:    internal.WorktreeAlways,
				ActivityTimeout: tt.timeout,
			},
		}
		err := cfg.Validate()
		gotErr := err != nil
		if gotErr != tt.wantErr {
			t.Errorf("Validate(timeout=%q) err=%v, wantErr=%v", tt.timeout, err, tt.wantErr)
		}
	}
}

// TestConfigValidateLoopThresholds verifies loop_warning < loop_interrupt.
func TestConfigValidateLoopThresholds(t *testing.T) {
	cfg := &Config{
		Execution: ExecutionConfig{
			Mode:          internal.ExecutionYolo,
			WorktreeMode:  internal.WorktreeAlways,
			LoopWarning:   5,
			LoopInterrupt: 3,
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Error("Expected error when loop_warning >= loop_interrupt")
	}
}

// TestConfigValidateCollectsAllErrors verifies multiple violations are collected.
func TestConfigValidateCollectsAllErrors(t *testing.T) {
	cfg := &Config{
		Execution: ExecutionConfig{
			Mode:            "bad",
			WorktreeMode:    "invalid",
			ActivityTimeout: "not-a-duration",
			LoopWarning:     5,
			LoopInterrupt:   3,
		},
	}
	err := cfg.Validate()
	if err == nil {
		t.Fatal("Expected validation errors")
	}
	ve, ok := err.(*internal.ValidationError)
	if !ok {
		t.Fatalf("Expected ValidationError, got %T", err)
	}
	if len(ve.ErrList) < 3 {
		t.Errorf("Expected at least 3 errors, got %d: %v", len(ve.ErrList), ve.ErrList)
	}
}

// TestGetProviderByID verifies provider lookup.
func TestGetProviderByID(t *testing.T) {
	cfg := &Config{
		Providers: []ProviderConfig{
			{ID: "openai"},
			{ID: "anthropic"},
		},
	}
	if p := cfg.GetProviderByID("openai"); p == nil {
		t.Error("GetProviderByID('openai') should find provider")
	}
	if p := cfg.GetProviderByID("nonexistent"); p != nil {
		t.Error("GetProviderByID('nonexistent') should return nil")
	}
}

// TestPreferredProvider verifies preferred provider selection.
func TestPreferredProvider(t *testing.T) {
	cfg := &Config{
		Providers: []ProviderConfig{
			{ID: "openai"},
			{ID: "anthropic", Preferred: true},
		},
	}
	p := cfg.PreferredProvider()
	if p == nil || p.ID != "anthropic" {
		t.Errorf("PreferredProvider = %v, want anthropic", p)
	}
}

// TestPreferredProviderEmpty verifies nil return when no providers.
func TestPreferredProviderEmpty(t *testing.T) {
	cfg := &Config{}
	if p := cfg.PreferredProvider(); p != nil {
		t.Error("PreferredProvider should return nil with no providers")
	}
}

// TestDeepMergeScalars verifies scalar fields are overwritten.
func TestDeepMergeScalars(t *testing.T) {
	base := &Config{ActiveProvider: "openai", ActiveModel: "gpt-4o"}
	override := &Config{ActiveProvider: "anthropic"}
	base.DeepMerge(override)
	if base.ActiveProvider != "anthropic" {
		t.Errorf("ActiveProvider = %q, want %q", base.ActiveProvider, "anthropic")
	}
	// Existing field not in override should be preserved
	if base.ActiveModel != "gpt-4o" {
		t.Errorf("ActiveModel = %q, want %q", base.ActiveModel, "gpt-4o")
	}
}

// TestDeepMergeProviders verifies provider merging by ID.
func TestDeepMergeProviders(t *testing.T) {
	base := &Config{Providers: []ProviderConfig{{ID: "openai", Name: "OpenAI"}}}
	override := &Config{Providers: []ProviderConfig{{ID: "openai", Name: "OpenAI Updated"}, {ID: "anthropic", Name: "Anthropic"}}}
	base.DeepMerge(override)
	if len(base.Providers) != 2 {
		t.Fatalf("Providers = %d, want 2", len(base.Providers))
	}
	if base.Providers[0].Name != "OpenAI Updated" {
		t.Errorf("Provider openai Name = %q, want %q", base.Providers[0].Name, "OpenAI Updated")
	}
}

// TestDeepMergeFontStyles verifies font_styles survive the config cascade: a
// layer that omits font_styles must not clobber one that set them (the bug
// where a project config's tui: section reset the home layer's italic:false).
func TestDeepMergeFontStyles(t *testing.T) {
	false_ := false
	true_ := true
	base := &Config{}
	base.TUI.FontStyles.Italic = &false_
	// Override layer sets theme but NO font_styles — Italic must be preserved.
	override := &Config{}
	override.TUI.Theme = "dark"
	base.DeepMerge(override)
	if base.TUI.FontStyles.ItalicEnabled() {
		t.Error("Italic=false from the base layer was clobbered by a tui: override without font_styles")
	}
	// A layer that DOES set font_styles overrides per-style.
	override2 := &Config{}
	override2.TUI.FontStyles.Italic = &true_
	override2.TUI.FontStyles.Bold = &false_
	base.DeepMerge(override2)
	if !base.TUI.FontStyles.ItalicEnabled() {
		t.Error("explicit italic=true override not applied")
	}
	if base.TUI.FontStyles.BoldEnabled() {
		t.Error("explicit bold=false override not applied")
	}
}

// TestDeepMergeSkillsDirs verifies Skills.Dirs concatenation.
func TestDeepMergeSkillsDirs(t *testing.T) {
	base := &Config{Skills: SkillsConfig{Dirs: []string{"dir1"}}}
	override := &Config{Skills: SkillsConfig{Dirs: []string{"dir2"}}}
	base.DeepMerge(override)
	if len(base.Skills.Dirs) != 2 {
		t.Fatalf("Skills.Dirs = %d, want 2", len(base.Skills.Dirs))
	}
}

// TestDeepCopy verifies that DeepCopy creates an independent copy.
func TestDeepCopy(t *testing.T) {
	original := &Config{ActiveProvider: "openai", ActiveModel: "gpt-4o"}
	copy := original.DeepCopy()
	copy.ActiveProvider = "anthropic"
	if original.ActiveProvider != "openai" {
		t.Error("DeepCopy should not share state with original")
	}
}

// --- M13: ModeConfig tests ---

func TestDefaultModeState_UsesExplicitDefault(t *testing.T) {
	cfg := &Config{
		Mode: ModeConfig{
			Default: internal.ModeState{
				Major:    internal.MajorCoder,
				Skills:   []string{"test-gen"},
				Autonomy: internal.AutonomyConfirm,
			},
		},
	}
	ms := cfg.DefaultModeState()
	if ms.Major != internal.MajorCoder {
		t.Errorf("Major = %q, want %q", ms.Major, internal.MajorCoder)
	}
	if ms.Autonomy != internal.AutonomyConfirm {
		t.Errorf("Autonomy = %q, want %q", ms.Autonomy, internal.AutonomyConfirm)
	}
	if len(ms.Skills) != 1 || ms.Skills[0] != "test-gen" {
		t.Errorf("Skills = %v, want [test-gen]", ms.Skills)
	}
}

func TestDefaultModeState_DefaultsFromConfigMajor(t *testing.T) {
	cfg := &Config{
		Mode: ModeConfig{
			Default: internal.ModeState{Major: internal.MajorPlanner},
			Defaults: map[internal.MajorMode]internal.AutonomyLevel{
				internal.MajorPlanner: internal.AutonomyConfirm,
			},
		},
	}
	ms := cfg.DefaultModeState()
	if ms.Major != internal.MajorPlanner {
		t.Errorf("Major = %q, want %q", ms.Major, internal.MajorPlanner)
	}
	if ms.Autonomy != internal.AutonomyConfirm {
		t.Errorf("Autonomy = %q, want %q", ms.Autonomy, internal.AutonomyConfirm)
	}
}

func TestDefaultModeState_FallbackToCoder(t *testing.T) {
	cfg := &Config{}
	ms := cfg.DefaultModeState()
	if ms.Major != internal.MajorCoder {
		t.Errorf("Major = %q, want %q", ms.Major, internal.MajorCoder)
	}
	if ms.Autonomy != internal.AutonomySolo {
		t.Errorf("Autonomy = %q, want %q", ms.Autonomy, internal.AutonomySolo)
	}
}

func TestDefaultModeState_ModeDefaultsOverExecutionMode(t *testing.T) {
	// mode.defaults takes priority over old execution.mode
	cfg := &Config{
		Execution: ExecutionConfig{Mode: internal.ExecutionYolo},
		Mode: ModeConfig{
			Defaults: map[internal.MajorMode]internal.AutonomyLevel{
				internal.MajorCoder: internal.AutonomyConfirm,
			},
		},
	}
	ms := cfg.DefaultModeState()
	if ms.Autonomy != internal.AutonomyConfirm {
		t.Errorf("Autonomy = %q, want %q (mode.defaults should win)", ms.Autonomy, internal.AutonomyConfirm)
	}
}

func TestModeConfigDeserializeFromYAML(t *testing.T) {
	y := `
mode:
  default:
    major: coder
    skills:
      - test-gen
    autonomy: yolo
  defaults:
    planner: review
    reviewer: confirm
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(y), &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}

	if cfg.Mode.Default.Major != internal.MajorCoder {
		t.Errorf("Mode.Default.Major = %q, want %q", cfg.Mode.Default.Major, internal.MajorCoder)
	}
	if cfg.Mode.Default.Autonomy != internal.AutonomyYolo {
		t.Errorf("Mode.Default.Autonomy = %q, want %q", cfg.Mode.Default.Autonomy, internal.AutonomyYolo)
	}
	if len(cfg.Mode.Default.Skills) != 1 || cfg.Mode.Default.Skills[0] != "test-gen" {
		t.Errorf("Mode.Default.Skills = %v, want [test-gen]", cfg.Mode.Default.Skills)
	}

	if cfg.Mode.Defaults == nil {
		t.Fatal("Mode.Defaults is nil")
	}
	if cfg.Mode.Defaults[internal.MajorPlanner] != internal.AutonomyReview {
		t.Errorf("Mode.Defaults[planner] = %q, want %q", cfg.Mode.Defaults[internal.MajorPlanner], internal.AutonomyReview)
	}
	if cfg.Mode.Defaults[internal.MajorReviewer] != internal.AutonomyConfirm {
		t.Errorf("Mode.Defaults[reviewer] = %q, want %q", cfg.Mode.Defaults[internal.MajorReviewer], internal.AutonomyConfirm)
	}
}

func TestModeConfigDeserializeEmpty(t *testing.T) {
	y := `{}`
	var cfg Config
	if err := yaml.Unmarshal([]byte(y), &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	// Mode should be zero value
	if cfg.Mode.Default.Major != "" {
		t.Errorf("Mode.Default.Major = %q, want empty", cfg.Mode.Default.Major)
	}
	if cfg.Mode.Defaults != nil {
		t.Errorf("Mode.Defaults = %v, want nil", cfg.Mode.Defaults)
	}
}

func TestDeepMerge_Mode(t *testing.T) {
	base := &Config{Mode: ModeConfig{Default: internal.ModeState{Major: internal.MajorCoder}}}
	override := &Config{
		Mode: ModeConfig{
			Default: internal.ModeState{Major: internal.MajorPlanner, Autonomy: internal.AutonomyConfirm},
			Defaults: map[internal.MajorMode]internal.AutonomyLevel{
				internal.MajorReviewer: internal.AutonomyYolo,
			},
		},
	}
	base.DeepMerge(override)

	if base.Mode.Default.Major != internal.MajorPlanner {
		t.Errorf("Mode.Default.Major = %q, want %q", base.Mode.Default.Major, internal.MajorPlanner)
	}
	if base.Mode.Default.Autonomy != internal.AutonomyConfirm {
		t.Errorf("Mode.Default.Autonomy = %q, want %q", base.Mode.Default.Autonomy, internal.AutonomyConfirm)
	}
	if base.Mode.Defaults == nil {
		t.Fatal("Mode.Defaults is nil after merge")
	}
	if base.Mode.Defaults[internal.MajorReviewer] != internal.AutonomyYolo {
		t.Errorf("Mode.Defaults[reviewer] = %q, want %q", base.Mode.Defaults[internal.MajorReviewer], internal.AutonomyYolo)
	}
}

func TestDeepMerge_ModeDefaultsPreserved(t *testing.T) {
	// When override doesn't have mode section, existing mode should be preserved
	base := &Config{
		Mode: ModeConfig{
			Defaults: map[internal.MajorMode]internal.AutonomyLevel{
				internal.MajorCoder: internal.AutonomyYolo,
			},
		},
	}
	override := &Config{ActiveProvider: "test"}
	base.DeepMerge(override)

	if len(base.Mode.Defaults) != 1 {
		t.Errorf("Mode.Defaults = %v, want 1 entry", base.Mode.Defaults)
	}
	if base.Mode.Defaults[internal.MajorCoder] != internal.AutonomyYolo {
		t.Errorf("Mode.Defaults[coder] = %q, want %q", base.Mode.Defaults[internal.MajorCoder], internal.AutonomyYolo)
	}
}

func TestDefaultModeState_MergesFromModeDefaults(t *testing.T) {
	// Test DefaultModeState with mode.default.major and mode.defaults
	cfg := &Config{
		Mode: ModeConfig{
			Default: internal.ModeState{Major: internal.MajorReviewer},
			Defaults: map[internal.MajorMode]internal.AutonomyLevel{
				internal.MajorReviewer: internal.AutonomyConfirm,
			},
		},
	}
	ms := cfg.DefaultModeState()
	if ms.Major != internal.MajorReviewer {
		t.Errorf("Major = %q, want %q", ms.Major, internal.MajorReviewer)
	}
	if ms.Autonomy != internal.AutonomyConfirm {
		t.Errorf("Autonomy = %q, want %q", ms.Autonomy, internal.AutonomyConfirm)
	}
}

func TestDefaultAutonomyForMajor_Defaults(t *testing.T) {
	tests := []struct {
		major internal.MajorMode
		want  internal.AutonomyLevel
	}{
		{internal.MajorCoder, internal.AutonomySolo},
		{internal.MajorPlanner, internal.AutonomySolo},
		{internal.MajorReviewer, internal.AutonomySolo},
		{"unknown", internal.AutonomySolo},
	}
	for _, tt := range tests {
		got := DefaultAutonomyForMajor(tt.major)
		if got != tt.want {
			t.Errorf("DefaultAutonomyForMajor(%q) = %q, want %q", tt.major, got, tt.want)
		}
	}
}

func TestDefaultModeState_FallbackBuiltinDefaults(t *testing.T) {
	// When no mode config at all, fall back to the generic SOLO default.
	// Mode-specific defaults are supplied by the mode registry at runtime.
	cfg := &Config{Mode: ModeConfig{Default: internal.ModeState{Major: internal.MajorPlanner}}}
	ms := cfg.DefaultModeState()
	if ms.Major != internal.MajorPlanner {
		t.Errorf("Major = %q, want %q", ms.Major, internal.MajorPlanner)
	}
	if ms.Autonomy != internal.AutonomySolo {
		t.Errorf("Autonomy = %q, want %q", ms.Autonomy, internal.AutonomySolo)
	}

	cfg2 := &Config{}
	ms2 := cfg2.DefaultModeState()
	if ms2.Autonomy != internal.AutonomySolo {
		t.Errorf("coder Autonomy = %q, want %q", ms2.Autonomy, internal.AutonomySolo)
	}
}

func TestDefaultModeState_UsesModeDefaults(t *testing.T) {
	cfg := &Config{
		Mode: ModeConfig{
			Default: internal.ModeState{Major: internal.MajorPlanner},
			Defaults: map[internal.MajorMode]internal.AutonomyLevel{
				internal.MajorPlanner: internal.AutonomyReview,
			},
		},
	}
	ms := cfg.DefaultModeState()
	if ms.Autonomy != internal.AutonomyReview {
		t.Errorf("Autonomy = %q, want %q", ms.Autonomy, internal.AutonomyReview)
	}
}

// --- Agentic config mapping tests ---

func TestConfigDeserialize_AgenticFields(t *testing.T) {
	cfg := unmarshalAgenticConfig(t)
	assertProvider(t, cfg)
	assertModel(t, cfg)
	assertExecution(t, cfg)
	assertContextCompression(t, cfg)
}

func unmarshalAgenticConfig(t *testing.T) Config {
	t.Helper()
	y := agenticConfigYAML()
	var cfg Config
	if err := yaml.Unmarshal([]byte(y), &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	return cfg
}

func agenticConfigYAML() string {
	return `
providers:
  - id: openai
    provider: openai
    api: openai-completions
    base_url: https://api.openai.com/v1
    transport: sse
    cache_retention: short
    session_id: sess-1
    metadata:
      project: goa
    max_retry_delay: 2s
    reasoning_effort: low
models:
  - id: gpt-4o
    provider: openai
    model: gpt-4o
    api: openai-completions
    provider_name: openai
    reasoning: true
    thinking_level: medium
    thinking_budget: 512
    input_types:
      - text
    headers:
      X-Model: "1"
    compat: '{"toolResultAsUser":true}'
execution:
  mode: yolo
  max_tool_repeat_total: 5
skills:
  execution_mode: inline
context_compression:
  enabled: true
  max_tokens: 8192
  threshold_percent: 80
  on_context_error: true
  strategy: tool_elision
  preserve_recent_turns: 3
`
}

func assertProvider(t *testing.T, cfg Config) {
	t.Helper()
	if len(cfg.Providers) != 1 {
		t.Fatalf("Providers = %d, want 1", len(cfg.Providers))
	}
	p := cfg.Providers[0]
	if p.Provider != AgenticProviderOpenAI {
		t.Errorf("Provider.Provider = %q, want %q", p.Provider, AgenticProviderOpenAI)
	}
	if p.API != AgenticAPIOpenAICompletions {
		t.Errorf("Provider.API = %q, want %q", p.API, AgenticAPIOpenAICompletions)
	}
	if p.Transport != AgenticTransportSSE {
		t.Errorf("Provider.Transport = %q, want %q", p.Transport, AgenticTransportSSE)
	}
	if p.CacheRetention != AgenticCacheRetentionShort {
		t.Errorf("Provider.CacheRetention = %q, want %q", p.CacheRetention, AgenticCacheRetentionShort)
	}
	if p.SessionID != "sess-1" {
		t.Errorf("Provider.SessionID = %q, want sess-1", p.SessionID)
	}
	if p.Metadata["project"] != "goa" {
		t.Errorf("Provider.Metadata project = %q, want goa", p.Metadata["project"])
	}
	if p.MaxRetryDelay != "2s" {
		t.Errorf("Provider.MaxRetryDelay = %q, want 2s", p.MaxRetryDelay)
	}
	if p.ReasoningEffort != "low" {
		t.Errorf("Provider.ReasoningEffort = %q, want low", p.ReasoningEffort)
	}
}

func assertModel(t *testing.T, cfg Config) {
	t.Helper()
	if len(cfg.Models) != 1 {
		t.Fatalf("Models = %d, want 1", len(cfg.Models))
	}
	m := cfg.Models[0]
	if !m.Reasoning {
		t.Error("Model.Reasoning should be true")
	}
	if m.ThinkingLevel != AgenticThinkingMedium {
		t.Errorf("Model.ThinkingLevel = %q, want %q", m.ThinkingLevel, AgenticThinkingMedium)
	}
	if m.ThinkingBudget != 512 {
		t.Errorf("Model.ThinkingBudget = %d, want 512", m.ThinkingBudget)
	}
	if len(m.InputTypes) != 1 || m.InputTypes[0] != "text" {
		t.Errorf("Model.InputTypes = %v, want [text]", m.InputTypes)
	}
	if m.Headers["X-Model"] != "1" {
		t.Errorf("Model.Headers X-Model = %q, want 1", m.Headers["X-Model"])
	}
}

func assertExecution(t *testing.T, cfg Config) {
	t.Helper()
	if cfg.Execution.MaxToolRepeatTotal != 5 {
		t.Errorf("Execution.MaxToolRepeatTotal = %d, want 5", cfg.Execution.MaxToolRepeatTotal)
	}
	if cfg.Skills.ExecutionMode != AgenticSkillModeInline {
		t.Errorf("Skills.ExecutionMode = %q, want %q", cfg.Skills.ExecutionMode, AgenticSkillModeInline)
	}
}

// TestMergeExecution_ToolCallLimitResetWindow verifies the new execution
// field is merged across config layers.
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

func assertContextCompression(t *testing.T, cfg Config) {
	t.Helper()
	if !cfg.ContextCompression.Enabled {
		t.Error("ContextCompression.Enabled should be true")
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
					Enabled:          true,
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
					Enabled:  true,
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
					Enabled:          true,
					ThresholdPercent: 150,
				},
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

	// Ambiguous: multiple models match the active provider without active_model
	cfg = &Config{
		ActiveProvider: "x",
		Providers:      []ProviderConfig{{ID: "x"}},
		Models: []ModelConfig{
			{ID: "m1", ProviderID: "x"},
			{ID: "m2", ProviderID: "x"},
		},
	}
	if _, err := cfg.GetActiveModelConfig(); err == nil {
		t.Error("expected error when multiple models match provider")
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
			Enabled:             true,
			MaxTokens:           4096,
			ThresholdPercent:    75,
			OnContextError:      true,
			Strategy:            AgenticCompressionSelective,
			PreserveRecentTurns: 4,
		},
	}
	base.DeepMerge(override)
	if !base.ContextCompression.Enabled {
		t.Error("ContextCompression.Enabled should be true")
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
	if cfg.Tools.Enabled.PTYExec {
		t.Error("PTYExec should be disabled by default")
	}
	if cfg.Tools.Enabled.SSHBash {
		t.Error("SSHBash should be disabled by default")
	}
	if cfg.Tools.Enabled.Memento {
		t.Error("Memento should be disabled by default")
	}
	if cfg.Tools.Enabled.DelegateTo {
		t.Error("DelegateTo should be disabled by default")
	}
	if cfg.Tools.Enabled.RequestReview {
		t.Error("RequestReview should be disabled by default")
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

// TestAgentToolsDefaultEnabled verifies the embedded default config ships the
// sub-agent/swarm/goa tools enabled (opt-out), preserving current behavior.
func TestAgentToolsDefaultEnabled(t *testing.T) {
	yamlText, err := DefaultConfigYAML()
	if err != nil {
		t.Fatalf("load embedded default: %v", err)
	}
	var cfg Config
	if err := yaml.Unmarshal([]byte(yamlText), &cfg); err != nil {
		t.Fatalf("Unmarshal embedded default failed: %v", err)
	}
	for _, name := range []string{"agent", "agent_swarm", "goa"} {
		if !cfg.Tools.Enabled.GetEnabled(name) {
			t.Errorf("embedded default should enable %s (opt-out)", name)
		}
	}
}

func TestToolEnabledConfigRoundTrip(t *testing.T) {
	y := `
tools:
  enabled:
    bg_exec: true
    pty_exec: true
    memento: true
`
	var cfg Config
	if err := yaml.Unmarshal([]byte(y), &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if !cfg.Tools.Enabled.BGExec {
		t.Error("BGExec should be true")
	}
	if !cfg.Tools.Enabled.PTYExec {
		t.Error("PTYExec should be true")
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
	want := base + "/.goa/plan.md"
	if got != want {
		t.Errorf("ResolvePlanFilePath = %q, want %q", got, want)
	}
}

func TestResolvePlanFilePath_ExplicitPath(t *testing.T) {
	base := t.TempDir()
	cfg := Config{Mode: ModeConfig{PlanFilePath: "plans/my-plan.md"}}
	got := cfg.ResolvePlanFilePath(base)
	want := base + "/plans/my-plan.md"
	if got != want {
		t.Errorf("ResolvePlanFilePath = %q, want %q", got, want)
	}
}

func TestResolvePlanFilePath_AbsolutePath(t *testing.T) {
	cfg := Config{Mode: ModeConfig{PlanFilePath: "/tmp/plans/plan.md"}}
	got := cfg.ResolvePlanFilePath("/should/be/ignored")
	want := "/tmp/plans/plan.md"
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
