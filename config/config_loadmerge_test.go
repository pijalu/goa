// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"testing"

	"github.com/pijalu/goa/internal"
)

// TestConfigDeserializeFromYAML verifies Config struct deserializes from YAML.

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
