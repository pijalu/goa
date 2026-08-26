// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"testing"

	"gopkg.in/yaml.v3"

	"github.com/pijalu/goa/internal"
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

// TestConfigValidateStreamLoopMinPeriod verifies execution.stream_loop_min_period
// accepts 0 (default) and values >= 8, rejecting anything below the absolute
// scan floor.
func TestConfigValidateMaxInlineBytes(t *testing.T) {
	for _, tt := range []struct {
		v       int
		wantErr bool
	}{
		{0, false},    // disabled (default)
		{4096, false}, // typical cap
		{-1, true},    // negative would spill every result
		{-4096, true},
	} {
		cfg := &Config{Tools: ToolsConfig{MaxInlineBytes: tt.v}}
		err := cfg.Validate()
		if gotErr := err != nil; gotErr != tt.wantErr {
			t.Errorf("Validate(max_inline_bytes=%d) err=%v, wantErr=%v", tt.v, err, tt.wantErr)
		}
	}
}

func TestConfigMaxInlineBytesYAML(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal([]byte("tools:\n  max_inline_bytes: 4096\n"), &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if cfg.Tools.MaxInlineBytes != 4096 {
		t.Errorf("Tools.MaxInlineBytes = %d, want 4096", cfg.Tools.MaxInlineBytes)
	}
}

func TestConfigValidateStreamLoopMinPeriod(t *testing.T) {
	for _, tt := range []struct {
		v       int
		wantErr bool
	}{
		{0, false},  // default (50)
		{8, false},  // absolute scan floor
		{50, false}, // default value spelled out
		{4096, false},
		{7, true},
		{1, true},
		{-1, true},
	} {
		cfg := &Config{Execution: ExecutionConfig{StreamLoopMinPeriod: tt.v}}
		err := cfg.Validate()
		if gotErr := err != nil; gotErr != tt.wantErr {
			t.Errorf("Validate(stream_loop_min_period=%d) err=%v, wantErr=%v", tt.v, err, tt.wantErr)
		}
	}
}

func TestConfigValidateRunawayLoopMaxRepeats(t *testing.T) {
	for _, tt := range []struct {
		v       int
		wantErr bool
	}{
		{0, false},  // default (2)
		{1, false},  // strict: stop at the first repeat
		{2, false},  // default value spelled out
		{10, false}, // lenient
		{-1, true},  // negative is never meaningful
		{-10, true},
	} {
		cfg := &Config{Execution: ExecutionConfig{RunawayLoopMaxRepeats: tt.v}}
		err := cfg.Validate()
		if gotErr := err != nil; gotErr != tt.wantErr {
			t.Errorf("Validate(runaway_loop_max_repeats=%d) err=%v, wantErr=%v", tt.v, err, tt.wantErr)
		}
	}
}

func TestRunawayLoopMaxRepeatsYAML(t *testing.T) {
	var cfg Config
	if err := yaml.Unmarshal([]byte("execution:\n  runaway_loop_max_repeats: 4\n"), &cfg); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if cfg.Execution.RunawayLoopMaxRepeats != 4 {
		t.Errorf("Execution.RunawayLoopMaxRepeats = %d, want 4", cfg.Execution.RunawayLoopMaxRepeats)
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
