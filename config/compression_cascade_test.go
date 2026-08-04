// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"os"
	"path/filepath"
	"testing"
)

// TestDeepMergeContextCompressionStrategies verifies that the per-layer
// strategies block merges field-wise across cascade layers instead of being
// silently dropped (bugs.md: strategies never merged).
func TestDeepMergeContextCompressionStrategies(t *testing.T) {
	base := &Config{ContextCompression: ContextCompressionConfig{
		Enabled: boolPtr(true),
		Strategies: CompressionLayerStrategiesConfig{Soft: "micro", Trigger: "tool_elision", Hard: "hybrid"},
	}}
	override := &Config{ContextCompression: ContextCompressionConfig{
		Enabled: boolPtr(true),
		Strategies: CompressionLayerStrategiesConfig{Trigger: "summarize"},
	}}
	base.DeepMerge(override)

	got := base.ContextCompression.Strategies
	if got.Trigger != "summarize" {
		t.Errorf("Strategies.Trigger = %q, want summarize (overridden)", got.Trigger)
	}
	if got.Soft != "micro" {
		t.Errorf("Strategies.Soft = %q, want micro (preserved)", got.Soft)
	}
	if got.Hard != "hybrid" {
		t.Errorf("Strategies.Hard = %q, want hybrid (preserved)", got.Hard)
	}
}

// TestDeepMergeContextCompressionMicroCompactionFieldWise verifies that
// micro_compaction merges field-wise: a higher layer setting one key does not
// reset the others (bugs.md: micro_compaction replaced wholesale).
func TestDeepMergeContextCompressionMicroCompactionFieldWise(t *testing.T) {
	base := &Config{ContextCompression: ContextCompressionConfig{
		Enabled: boolPtr(true),
		MicroCompaction: MicroCompactionSettings{KeepRecentMessages: 30, MinContextRatio: 0.6, CacheMissThreshold: "1h"},
	}}
	override := &Config{ContextCompression: ContextCompressionConfig{
		Enabled: boolPtr(true),
		MicroCompaction: MicroCompactionSettings{MinContextRatio: 0.4},
	}}
	base.DeepMerge(override)

	got := base.ContextCompression.MicroCompaction
	if got.MinContextRatio != 0.4 {
		t.Errorf("MicroCompaction.MinContextRatio = %v, want 0.4 (overridden)", got.MinContextRatio)
	}
	if got.KeepRecentMessages != 30 {
		t.Errorf("MicroCompaction.KeepRecentMessages = %d, want 30 (preserved)", got.KeepRecentMessages)
	}
	if got.CacheMissThreshold != "1h" {
		t.Errorf("MicroCompaction.CacheMissThreshold = %q, want 1h (preserved)", got.CacheMissThreshold)
	}
}

// TestDeepMergeContextCompressionEnabledTriState verifies that an explicit
// enabled: false in a higher cascade layer disables compression, while a
// layer that leaves enabled unset preserves the lower layer's value
// (bugs.md: enabled: false ignored).
func TestDeepMergeContextCompressionEnabledTriState(t *testing.T) {
	on := true
	off := false

	// Explicit false in a higher layer disables.
	base := &Config{ContextCompression: ContextCompressionConfig{Enabled: &on, ThresholdPercent: 80}}
	override := &Config{ContextCompression: ContextCompressionConfig{Enabled: &off}}
	base.DeepMerge(override)
	if base.ContextCompression.EnabledValue() {
		t.Error("EnabledValue() = true after explicit false override, want false")
	}

	// Unset (nil) in a higher layer preserves the lower layer's true.
	base = &Config{ContextCompression: ContextCompressionConfig{Enabled: &on, ThresholdPercent: 80}}
	override = &Config{ContextCompression: ContextCompressionConfig{ThresholdPercent: 90}}
	base.DeepMerge(override)
	if !base.ContextCompression.EnabledValue() {
		t.Error("EnabledValue() = false after nil override, want true (preserved)")
	}

	// Unset (nil) in a higher layer preserves the lower layer's false.
	base = &Config{ContextCompression: ContextCompressionConfig{Enabled: &off}}
	override = &Config{ContextCompression: ContextCompressionConfig{ThresholdPercent: 90}}
	base.DeepMerge(override)
	if base.ContextCompression.EnabledValue() {
		t.Error("EnabledValue() = true after nil override, want false (preserved)")
	}

	// Higher-layer settings still merge when compression stays enabled.
	base = &Config{ContextCompression: ContextCompressionConfig{Enabled: &on}}
	override = &Config{ContextCompression: ContextCompressionConfig{ThresholdPercent: 90}}
	base.DeepMerge(override)
	if base.ContextCompression.ThresholdPercent != 90 {
		t.Errorf("ThresholdPercent = %d, want 90 (merged while enabled)", base.ContextCompression.ThresholdPercent)
	}
}

// TestCascadeContextCompressionStrategiesSurviveLoad verifies the strategies
// block in a home config file survives the cascade load (not dropped).
func TestCascadeContextCompressionStrategiesSurviveLoad(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	goaDir := filepath.Join(home, ".goa")
	if err := os.MkdirAll(goaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	homeYaml := `
context_compression:
  enabled: true
  strategies:
    trigger: summarize
micro_compaction: {}
`
	if err := os.WriteFile(filepath.Join(goaDir, "config.yaml"), []byte(homeYaml), 0o644); err != nil {
		t.Fatal(err)
	}
	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ContextCompression.Strategies.Trigger != "summarize" {
		t.Errorf("Strategies.Trigger = %q, want summarize (from home layer)", cfg.ContextCompression.Strategies.Trigger)
	}
}

// TestCascadeContextCompressionEnabledFalseDisables verifies that
// context_compression.enabled: false in the home file disables compression
// on load (bugs.md: enabled: false was a no-op).
func TestCascadeContextCompressionEnabledFalseDisables(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	goaDir := filepath.Join(home, ".goa")
	if err := os.MkdirAll(goaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	homeYaml := `
context_compression:
  enabled: false
`
	if err := os.WriteFile(filepath.Join(goaDir, "config.yaml"), []byte(homeYaml), 0o644); err != nil {
		t.Fatal(err)
	}
	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.ContextCompression.EnabledValue() {
		t.Error("EnabledValue() = true with home enabled: false, want false")
	}
}

// TestCascadeContextCompressionMicroFieldWiseAcrossLayers verifies
// micro_compaction keys merge field-wise between embedded defaults and a
// home file that sets only one key.
func TestCascadeContextCompressionMicroFieldWiseAcrossLayers(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	goaDir := filepath.Join(home, ".goa")
	if err := os.MkdirAll(goaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	homeYaml := `
context_compression:
  enabled: true
  micro_compaction:
    min_context_ratio: 0.45
`
	if err := os.WriteFile(filepath.Join(goaDir, "config.yaml"), []byte(homeYaml), 0o644); err != nil {
		t.Fatal(err)
	}
	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	mc := cfg.ContextCompression.MicroCompaction
	if mc.MinContextRatio != 0.45 {
		t.Errorf("MinContextRatio = %v, want 0.45 (overridden)", mc.MinContextRatio)
	}
	// The embedded default for keep_recent_messages (if any) must survive.
	// Assert it is not zeroed by the partial home layer.
	if mc.KeepRecentMessages == 0 {
		t.Errorf("KeepRecentMessages = 0 after partial home override; embedded default was wiped")
	}
}
