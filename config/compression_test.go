// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"strings"
	"testing"
)

// TestDeepMergeContextCompressionThresholds verifies that threshold fields
// merge field-wise across cascade layers: higher layers override only the
// fields they set.
func TestDeepMergeContextCompressionThresholds(t *testing.T) {
	base := &Config{ContextCompression: ContextCompressionConfig{
		Enabled:          true,
		ThresholdPercent: 80,
		Thresholds:       CompressionThresholdsConfig{SoftPercent: 50, TriggerPercent: 75, HardPercent: 95},
	}}
	override := &Config{ContextCompression: ContextCompressionConfig{
		Enabled:    true,
		Thresholds: CompressionThresholdsConfig{TriggerPercent: 85},
	}}
	base.DeepMerge(override)

	got := base.ContextCompression.Thresholds
	if got.TriggerPercent != 85 {
		t.Errorf("TriggerPercent = %d, want 85 (overridden)", got.TriggerPercent)
	}
	if got.SoftPercent != 50 {
		t.Errorf("SoftPercent = %d, want 50 (preserved)", got.SoftPercent)
	}
	if got.HardPercent != 95 {
		t.Errorf("HardPercent = %d, want 95 (preserved)", got.HardPercent)
	}
	// Legacy scalar: override layer left it zero → base value preserved.
	if base.ContextCompression.ThresholdPercent != 80 {
		t.Errorf("ThresholdPercent = %d, want 80 (preserved)", base.ContextCompression.ThresholdPercent)
	}
}

// TestDeepMergeContextCompressionPerModel verifies per-model override entries
// merge by model ID, field-wise, so a higher layer can tune one field without
// restating the whole entry.
func TestDeepMergeContextCompressionPerModel(t *testing.T) {
	base := &Config{ContextCompression: ContextCompressionConfig{
		Enabled: true,
		PerModel: map[string]ModelCompressionOverride{
			"local-qwen": {MaxTokens: 24576, Strategy: "hybrid"},
		},
	}}
	override := &Config{ContextCompression: ContextCompressionConfig{
		Enabled: true,
		PerModel: map[string]ModelCompressionOverride{
			"local-qwen": {Thresholds: CompressionThresholdsConfig{TriggerPercent: 65}},
			"claude":     {Thresholds: CompressionThresholdsConfig{TriggerPercent: 90}},
		},
	}}
	base.DeepMerge(override)

	pm := base.ContextCompression.PerModel
	if len(pm) != 2 {
		t.Fatalf("PerModel len = %d, want 2", len(pm))
	}
	q := pm["local-qwen"]
	if q.MaxTokens != 24576 {
		t.Errorf("local-qwen MaxTokens = %d, want 24576 (preserved)", q.MaxTokens)
	}
	if q.Strategy != "hybrid" {
		t.Errorf("local-qwen Strategy = %q, want hybrid (preserved)", q.Strategy)
	}
	if q.Thresholds.TriggerPercent != 65 {
		t.Errorf("local-qwen TriggerPercent = %d, want 65 (overridden)", q.Thresholds.TriggerPercent)
	}
	if pm["claude"].Thresholds.TriggerPercent != 90 {
		t.Errorf("claude TriggerPercent = %d, want 90 (added)", pm["claude"].Thresholds.TriggerPercent)
	}
}

// TestDeepMergeContextCompressionMicroCompaction verifies micro compaction
// settings from a higher cascade layer are carried over (previously dropped).
func TestDeepMergeContextCompressionMicroCompaction(t *testing.T) {
	base := &Config{ContextCompression: ContextCompressionConfig{Enabled: true}}
	override := &Config{ContextCompression: ContextCompressionConfig{
		Enabled:         true,
		MicroCompaction: MicroCompactionSettings{KeepRecentMessages: 30, MinContextRatio: 0.6},
	}}
	base.DeepMerge(override)
	if base.ContextCompression.MicroCompaction.KeepRecentMessages != 30 {
		t.Errorf("MicroCompaction.KeepRecentMessages = %d, want 30", base.ContextCompression.MicroCompaction.KeepRecentMessages)
	}
}

// TestConfigValidateCompressionThresholds covers range and ordering checks
// for the tiered thresholds, globally and per model.
func TestConfigValidateCompressionThresholds(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ContextCompressionConfig
		models  []ModelConfig
		wantErr string // substring; empty = valid
	}{
		{
			name: "valid tiers",
			cfg: ContextCompressionConfig{
				Enabled:    true,
				Thresholds: CompressionThresholdsConfig{SoftPercent: 50, TriggerPercent: 80, HardPercent: 95},
			},
		},
		{
			name: "soft above trigger rejected",
			cfg: ContextCompressionConfig{
				Enabled:    true,
				Thresholds: CompressionThresholdsConfig{SoftPercent: 85, TriggerPercent: 80},
			},
			wantErr: "soft_percent (85) must be ≤ trigger_percent (80)",
		},
		{
			name: "trigger above hard rejected",
			cfg: ContextCompressionConfig{
				Enabled:    true,
				Thresholds: CompressionThresholdsConfig{TriggerPercent: 96, HardPercent: 95},
			},
			wantErr: "trigger_percent (96) must be ≤ hard_percent (95)",
		},
		{
			name: "out of range rejected",
			cfg: ContextCompressionConfig{
				Enabled:    true,
				Thresholds: CompressionThresholdsConfig{HardPercent: 101},
			},
			wantErr: "hard_percent: must be 0-100",
		},
		{
			name: "negative rejected",
			cfg: ContextCompressionConfig{
				Enabled:    true,
				Thresholds: CompressionThresholdsConfig{SoftPercent: -1},
			},
			wantErr: "soft_percent: must be 0-100",
		},
		{
			name: "unknown per-model key rejected",
			cfg: ContextCompressionConfig{
				Enabled:  true,
				PerModel: map[string]ModelCompressionOverride{"ghost": {MaxTokens: 1000}},
			},
			wantErr: `no model with id "ghost" is configured`,
		},
		{
			name: "known per-model key accepted",
			cfg: ContextCompressionConfig{
				Enabled:  true,
				PerModel: map[string]ModelCompressionOverride{"qwen": {MaxTokens: 1000}},
			},
			models: []ModelConfig{{ID: "qwen", ProviderID: "p", Model: "qwen3"}},
		},
		{
			name: "per-model thresholds validated",
			cfg: ContextCompressionConfig{
				Enabled: true,
				PerModel: map[string]ModelCompressionOverride{
					"qwen": {Thresholds: CompressionThresholdsConfig{TriggerPercent: 99, HardPercent: 90}},
				},
			},
			models:  []ModelConfig{{ID: "qwen", ProviderID: "p", Model: "qwen3"}},
			wantErr: "trigger_percent (99) must be ≤ hard_percent (90)",
		},
		{
			name: "per-model strategy validated",
			cfg: ContextCompressionConfig{
				Enabled:  true,
				PerModel: map[string]ModelCompressionOverride{"qwen": {Strategy: "bogus"}},
			},
			models:  []ModelConfig{{ID: "qwen", ProviderID: "p", Model: "qwen3"}},
			wantErr: `strategy: unknown strategy "bogus"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{ContextCompression: tt.cfg, Models: tt.models}
			err := cfg.Validate()
			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() = %v, want nil", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Validate() = nil, want error containing %q", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Fatalf("Validate() = %v, want error containing %q", err, tt.wantErr)
			}
		})
	}
}

// TestDefaultConfig_CompressionThresholds verifies the embedded default
// config carries the new-style tiered thresholds (regression: the legacy
// threshold_percent key must no longer be the source of the 80% default).
func TestDefaultConfig_CompressionThresholds(t *testing.T) {
	// Isolate from the user's home config so only embedded defaults matter.
	t.Setenv("HOME", t.TempDir())
	loader := NewCascadeLoader(t.TempDir(), "", nil)
	cfg, err := loader.Load()
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	cc := cfg.ContextCompression
	if !cc.Enabled {
		t.Fatal("ContextCompression.Enabled = false, want true")
	}
	if cc.Thresholds.TriggerPercent != 80 {
		t.Errorf("Thresholds.TriggerPercent = %d, want 80", cc.Thresholds.TriggerPercent)
	}
	if cc.Thresholds.HardPercent != 95 {
		t.Errorf("Thresholds.HardPercent = %d, want 95", cc.Thresholds.HardPercent)
	}
	if cc.ThresholdPercent != 0 {
		t.Errorf("legacy ThresholdPercent = %d, want 0 (migrated to thresholds block)", cc.ThresholdPercent)
	}
}
