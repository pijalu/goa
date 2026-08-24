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

// TestDeepMergeStreamLoopMinPeriod verifies the overlay wins when non-zero
// and the base is preserved otherwise.
func TestDeepMergeStreamLoopMinPeriod(t *testing.T) {
	base := &Config{Execution: ExecutionConfig{StreamLoopMinPeriod: 80}}
	base.DeepMerge(&Config{Execution: ExecutionConfig{StreamLoopMinPeriod: 40}})
	if base.Execution.StreamLoopMinPeriod != 40 {
		t.Errorf("StreamLoopMinPeriod = %d, want 40", base.Execution.StreamLoopMinPeriod)
	}
	base.DeepMerge(&Config{})
	if base.Execution.StreamLoopMinPeriod != 40 {
		t.Errorf("StreamLoopMinPeriod after empty merge = %d, want 40 (preserved)", base.Execution.StreamLoopMinPeriod)
	}
}

// TestConfigValidateRunawayLoopMaxRepeats verifies execution.runaway_loop_max_repeats
// accepts 0 (default 2), the minimum meaningful limit 1, and larger values,
// rejecting negative values.
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

// TestDeepMergeRunawayLoopMaxRepeats verifies the overlay wins when non-zero
// and the base is preserved otherwise.
func TestDeepMergeRunawayLoopMaxRepeats(t *testing.T) {
	base := &Config{Execution: ExecutionConfig{RunawayLoopMaxRepeats: 4}}
	base.DeepMerge(&Config{Execution: ExecutionConfig{RunawayLoopMaxRepeats: 6}})
	if base.Execution.RunawayLoopMaxRepeats != 6 {
		t.Errorf("RunawayLoopMaxRepeats = %d, want 6", base.Execution.RunawayLoopMaxRepeats)
	}
	base.DeepMerge(&Config{})
	if base.Execution.RunawayLoopMaxRepeats != 6 {
		t.Errorf("RunawayLoopMaxRepeats after empty merge = %d, want 6 (preserved)", base.Execution.RunawayLoopMaxRepeats)
	}
}

// TestRunawayLoopMaxRepeatsYAML verifies the YAML key round-trips.
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

// TestDeepMergeSkillsDisabled verifies Skills.Disabled concatenates with
// dedup across the config cascade (embedded → home → project → local).
func TestDeepMergeSkillsDisabled(t *testing.T) {
	base := &Config{Skills: SkillsConfig{Disabled: []string{"dream"}}}
	override := &Config{Skills: SkillsConfig{Disabled: []string{"telegram", "dream"}}}
	base.DeepMerge(override)
	want := []string{"dream", "telegram"}
	if len(base.Skills.Disabled) != len(want) {
		t.Fatalf("Skills.Disabled = %v, want %v", base.Skills.Disabled, want)
	}
	for i, name := range want {
		if base.Skills.Disabled[i] != name {
			t.Errorf("Skills.Disabled[%d] = %q, want %q", i, base.Skills.Disabled[i], name)
		}
	}
}

// TestDeepMergeSkillsEnabled verifies Skills.Enabled concatenates with dedup
// across the config cascade (embedded → home → project → local).
func TestDeepMergeSkillsEnabled(t *testing.T) {
	base := &Config{Skills: SkillsConfig{Enabled: []string{"review"}}}
	override := &Config{Skills: SkillsConfig{Enabled: []string{"refactor", "review"}}}
	base.DeepMerge(override)
	want := []string{"review", "refactor"}
	if len(base.Skills.Enabled) != len(want) {
		t.Fatalf("Skills.Enabled = %v, want %v", base.Skills.Enabled, want)
	}
	for i, name := range want {
		if base.Skills.Enabled[i] != name {
			t.Errorf("Skills.Enabled[%d] = %q, want %q", i, base.Skills.Enabled[i], name)
		}
	}
}

// TestDeepMergeSkillsStickyLists verifies Skills.Sticky and Skills.StickyOff
// concatenate with dedup across the config cascade, mirroring the
// Enabled/Disabled semantics (project-level sticky state).
func TestDeepMergeSkillsStickyLists(t *testing.T) {
	base := &Config{Skills: SkillsConfig{Sticky: []string{"review"}, StickyOff: []string{"telegram"}}}
	override := &Config{Skills: SkillsConfig{Sticky: []string{"refactor", "review"}, StickyOff: []string{"telegram", "dream"}}}
	base.DeepMerge(override)
	wantSticky := []string{"review", "refactor"}
	wantOff := []string{"telegram", "dream"}
	if len(base.Skills.Sticky) != len(wantSticky) {
		t.Fatalf("Skills.Sticky = %v, want %v", base.Skills.Sticky, wantSticky)
	}
	for i, name := range wantSticky {
		if base.Skills.Sticky[i] != name {
			t.Errorf("Skills.Sticky[%d] = %q, want %q", i, base.Skills.Sticky[i], name)
		}
	}
	if len(base.Skills.StickyOff) != len(wantOff) {
		t.Fatalf("Skills.StickyOff = %v, want %v", base.Skills.StickyOff, wantOff)
	}
	for i, name := range wantOff {
		if base.Skills.StickyOff[i] != name {
			t.Errorf("Skills.StickyOff[%d] = %q, want %q", i, base.Skills.StickyOff[i], name)
		}
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

// deepCopyFixture returns a config exercising the fields a faithful deep
// copy must handle: a disabled ContextCompression block (DeepMerge would
// drop it), nested maps, a slice of structs, a *bool, and yaml:"-" fields.
func deepCopyFixture() *Config {
	stall := false
	return &Config{
		ContextCompression: ContextCompressionConfig{
			Enabled:    boolPtr(false), // disabled: explicit off survives merges
			Thresholds: CompressionThresholdsConfig{SoftPercent: 85, TriggerPercent: 80},
			PerModel:   map[string]ModelCompressionOverride{"m1": {MaxTokens: 4096}},
		},
		Execution: ExecutionConfig{DisableThinkingStallDetection: &stall},
		Providers: []ProviderConfig{{ID: "p1", Headers: map[string]string{"k": "v"}}},
		Aliases:   map[string]string{"n": "session:new"},
		FirstRun:  true,
		ConfigDir: "/tmp/x",
	}
}

// TestDeepCopy_Faithful pins fidelity: DeepCopy must preserve sections the
// cascade merge would skip (the ContextCompression block when disabled),
// nested reference content, and yaml:"-" fields.
func TestDeepCopy_Faithful(t *testing.T) {
	copy := deepCopyFixture().DeepCopy()

	if copy.ContextCompression.Thresholds != (CompressionThresholdsConfig{SoftPercent: 85, TriggerPercent: 80}) {
		t.Errorf("DeepCopy lost disabled ContextCompression thresholds: %+v", copy.ContextCompression)
	}
	if copy.ContextCompression.PerModel["m1"].MaxTokens != 4096 {
		t.Errorf("DeepCopy lost ContextCompression per-model overrides: %+v", copy.ContextCompression.PerModel)
	}
	if len(copy.Providers) != 1 || copy.Providers[0].ID != "p1" || copy.Providers[0].Headers["k"] != "v" {
		t.Errorf("DeepCopy lost provider slice content: %+v", copy.Providers)
	}
	if copy.Aliases["n"] != "session:new" {
		t.Errorf("DeepCopy lost aliases: %+v", copy.Aliases)
	}
	if copy.Execution.DisableThinkingStallDetection == nil || *copy.Execution.DisableThinkingStallDetection {
		t.Errorf("DeepCopy lost *bool field: %v", copy.Execution.DisableThinkingStallDetection)
	}
	if !copy.FirstRun || copy.ConfigDir != "/tmp/x" {
		t.Errorf("DeepCopy lost yaml:\"-\" fields: FirstRun=%v ConfigDir=%q", copy.FirstRun, copy.ConfigDir)
	}
}

// TestDeepCopy_NoAliasing pins independence: mutating the copy must not
// leak into the original (no shared maps, slices, or pointers).
func TestDeepCopy_NoAliasing(t *testing.T) {
	original := deepCopyFixture()
	copy := original.DeepCopy()

	copy.ContextCompression.PerModel["m1"] = ModelCompressionOverride{MaxTokens: 8192}
	copy.Providers[0].Headers["k"] = "changed"
	copy.Aliases["n"] = "changed"
	*copy.Execution.DisableThinkingStallDetection = true

	if original.ContextCompression.PerModel["m1"].MaxTokens != 4096 {
		t.Error("PerModel map aliased between original and copy")
	}
	if original.Providers[0].Headers["k"] != "v" {
		t.Error("provider Headers map aliased between original and copy")
	}
	if original.Aliases["n"] != "session:new" {
		t.Error("Aliases map aliased between original and copy")
	}
	if *original.Execution.DisableThinkingStallDetection {
		t.Error("*bool field aliased between original and copy")
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
    retry_policy:
      mode: always
      max_retries: 7
      backoff:
        initial_ms: 500
        max_ms: 5000
        jitter: 0.2
      codes:
        - RATE_LIMIT
        - SERVER
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
	if p.RetryPolicy == nil {
		t.Fatal("Provider.RetryPolicy should be set")
	}
	if p.RetryPolicy.Mode != "always" {
		t.Errorf("Provider.RetryPolicy.Mode = %q, want always", p.RetryPolicy.Mode)
	}
	if p.RetryPolicy.MaxRetries != 7 {
		t.Errorf("Provider.RetryPolicy.MaxRetries = %d, want 7", p.RetryPolicy.MaxRetries)
	}
	if p.RetryPolicy.Backoff.InitialMS != 500 {
		t.Errorf("Provider.RetryPolicy.Backoff.InitialMS = %d, want 500", p.RetryPolicy.Backoff.InitialMS)
	}
	if p.RetryPolicy.Backoff.MaxMS != 5000 {
		t.Errorf("Provider.RetryPolicy.Backoff.MaxMS = %d, want 5000", p.RetryPolicy.Backoff.MaxMS)
	}
	if p.RetryPolicy.Backoff.Jitter != 0.2 {
		t.Errorf("Provider.RetryPolicy.Backoff.Jitter = %v, want 0.2", p.RetryPolicy.Backoff.Jitter)
	}
	if len(p.RetryPolicy.Codes) != 2 || p.RetryPolicy.Codes[0] != "RATE_LIMIT" || p.RetryPolicy.Codes[1] != "SERVER" {
		t.Errorf("Provider.RetryPolicy.Codes = %v, want [RATE_LIMIT SERVER]", p.RetryPolicy.Codes)
	}
}

func assertModel(t *testing.T, cfg Config) {
	t.Helper()
	if len(cfg.Models) != 1 {
		t.Fatalf("Models = %d, want 1", len(cfg.Models))
	}
	m := cfg.Models[0]
	if m.Reasoning == nil || !*m.Reasoning {
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
