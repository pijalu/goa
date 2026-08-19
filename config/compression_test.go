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
		Enabled:          boolPtr(true),
		ThresholdPercent: 80,
		Thresholds:       CompressionThresholdsConfig{SoftPercent: 50, TriggerPercent: 75, HardPercent: 95},
	}}
	override := &Config{ContextCompression: ContextCompressionConfig{
		Enabled:    boolPtr(true),
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
		Enabled: boolPtr(true),
		PerModel: map[string]ModelCompressionOverride{
			"local-qwen": {MaxTokens: 24576, Strategy: "hybrid"},
		},
	}}
	override := &Config{ContextCompression: ContextCompressionConfig{
		Enabled: boolPtr(true),
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
	base := &Config{ContextCompression: ContextCompressionConfig{Enabled: boolPtr(true)}}
	override := &Config{ContextCompression: ContextCompressionConfig{
		Enabled:         boolPtr(true),
		MicroCompaction: MicroCompactionSettings{KeepRecentMessages: 30, MinContextRatio: 0.6},
	}}
	base.DeepMerge(override)
	if base.ContextCompression.MicroCompaction.KeepRecentMessages != 30 {
		t.Errorf("MicroCompaction.KeepRecentMessages = %d, want 30", base.ContextCompression.MicroCompaction.KeepRecentMessages)
	}
}

// TestDeepMergeContextCompressionOnErrorStrategy verifies OnErrorStrategy
// merges like the other non-empty-wins fields across cascade layers.
func TestDeepMergeContextCompressionOnErrorStrategy(t *testing.T) {
	base := &Config{ContextCompression: ContextCompressionConfig{
		Enabled:         boolPtr(true),
		OnErrorStrategy: "hybrid",
	}}
	override := &Config{ContextCompression: ContextCompressionConfig{
		Enabled: boolPtr(true),
		// OnErrorStrategy empty in the override layer → base value kept.
	}}
	base.DeepMerge(override)
	if base.ContextCompression.OnErrorStrategy != "hybrid" {
		t.Errorf("OnErrorStrategy = %q, want hybrid (preserved when override empty)", base.ContextCompression.OnErrorStrategy)
	}

	override.ContextCompression.OnErrorStrategy = "summarize"
	base.DeepMerge(override)
	if base.ContextCompression.OnErrorStrategy != "summarize" {
		t.Errorf("OnErrorStrategy = %q, want summarize (non-empty override wins)", base.ContextCompression.OnErrorStrategy)
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
				Enabled:    boolPtr(true),
				Thresholds: CompressionThresholdsConfig{SoftPercent: 50, TriggerPercent: 80, HardPercent: 95},
			},
		},
		{
			name: "soft above trigger rejected",
			cfg: ContextCompressionConfig{
				Enabled:    boolPtr(true),
				Thresholds: CompressionThresholdsConfig{SoftPercent: 85, TriggerPercent: 80},
			},
			wantErr: "soft_percent (85) must be ≤ trigger_percent (80)",
		},
		{
			name: "trigger above hard rejected",
			cfg: ContextCompressionConfig{
				Enabled:    boolPtr(true),
				Thresholds: CompressionThresholdsConfig{TriggerPercent: 96, HardPercent: 95},
			},
			wantErr: "trigger_percent (96) must be ≤ hard_percent (95)",
		},
		{
			name: "out of range rejected",
			cfg: ContextCompressionConfig{
				Enabled:    boolPtr(true),
				Thresholds: CompressionThresholdsConfig{HardPercent: 101},
			},
			wantErr: "hard_percent: must be 5-100 in 5% increments, 0 to disable",
		},
		{
			name: "soft disable (-1) accepted",
			cfg: ContextCompressionConfig{
				Enabled:    boolPtr(true),
				Thresholds: CompressionThresholdsConfig{SoftPercent: -1},
			},
		},
		{
			name: "soft negative below -1 rejected",
			cfg: ContextCompressionConfig{
				Enabled:    boolPtr(true),
				Thresholds: CompressionThresholdsConfig{SoftPercent: -2},
			},
			wantErr: "soft_percent: must be 5-100 in 5% increments, 0 to disable",
		},
		{
			name: "non 5-step increment rejected",
			cfg: ContextCompressionConfig{
				Enabled:    boolPtr(true),
				Thresholds: CompressionThresholdsConfig{TriggerPercent: 42},
			},
			wantErr: "trigger_percent: must be 5-100 in 5% increments, 0 to disable",
		},
		{
			name: "5 and 100 accepted (new opt-in range)",
			cfg: ContextCompressionConfig{
				Enabled:    boolPtr(true),
				Thresholds: CompressionThresholdsConfig{SoftPercent: 5, TriggerPercent: 100, HardPercent: 100},
			},
		},
		{
			name: "4 rejected (not a 5-step)",
			cfg: ContextCompressionConfig{
				Enabled:    boolPtr(true),
				Thresholds: CompressionThresholdsConfig{SoftPercent: 4},
			},
			wantErr: "soft_percent: must be 5-100 in 5% increments, 0 to disable",
		},
		{
			name: "valid per-layer strategies accepted",
			cfg: ContextCompressionConfig{
				Enabled:    boolPtr(true),
				Strategies: CompressionLayerStrategiesConfig{Soft: "micro", Trigger: "tool_elision", Hard: "hybrid"},
			},
		},
		{
			name: "soft layer accepts any strategy (all-methods soft)",
			cfg: ContextCompressionConfig{
				Enabled:    boolPtr(true),
				Strategies: CompressionLayerStrategiesConfig{Soft: "summarize"},
			},
		},
		{
			name: "unknown layer strategy rejected",
			cfg: ContextCompressionConfig{
				Enabled:    boolPtr(true),
				Strategies: CompressionLayerStrategiesConfig{Hard: "bogus"},
			},
			wantErr: `strategies.hard: unknown strategy "bogus"`,
		},
		{
			name: "unknown on_error_strategy rejected",
			cfg: ContextCompressionConfig{
				Enabled:         boolPtr(true),
				OnErrorStrategy: "bogus",
			},
			wantErr: `on_error_strategy: unknown strategy "bogus"`,
		},
		{
			name: "cache gate values accepted",
			cfg: ContextCompressionConfig{
				Enabled:   boolPtr(true),
				CacheGate: "off",
			},
		},
		{
			name: "invalid cache gate rejected",
			cfg: ContextCompressionConfig{
				Enabled:   boolPtr(true),
				CacheGate: "maybe",
			},
			wantErr: `cache_gate: must be "on" or "off"`,
		},
		{
			name: "unknown per-model key rejected",
			cfg: ContextCompressionConfig{
				Enabled:  boolPtr(true),
				PerModel: map[string]ModelCompressionOverride{"ghost": {MaxTokens: 1000}},
			},
			wantErr: `no model with id "ghost" is configured`,
		},
		{
			name: "known per-model key accepted",
			cfg: ContextCompressionConfig{
				Enabled:  boolPtr(true),
				PerModel: map[string]ModelCompressionOverride{"qwen": {MaxTokens: 1000}},
			},
			models: []ModelConfig{{ID: "qwen", ProviderID: "p", Model: "qwen3"}},
		},
		{
			name: "per-model thresholds validated",
			cfg: ContextCompressionConfig{
				Enabled: boolPtr(true),
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
				Enabled:  boolPtr(true),
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
	if !cc.EnabledValue() {
		t.Fatal("ContextCompression.EnabledValue() = false, want true")
	}
	// Opt-in semantics: soft/trigger default to 0 (disabled); the hard tier
	// is explicitly ON at 95 in the embedded default.yaml (no implicit engine
	// default-on), with summarize as its method and hybrid on-error recovery.
	if cc.Thresholds.SoftPercent != 0 {
		t.Errorf("Thresholds.SoftPercent = %d, want 0 (disabled by default)", cc.Thresholds.SoftPercent)
	}
	if cc.Thresholds.TriggerPercent != 0 {
		t.Errorf("Thresholds.TriggerPercent = %d, want 0 (disabled by default)", cc.Thresholds.TriggerPercent)
	}
	if cc.Thresholds.HardPercent != 95 {
		t.Errorf("Thresholds.HardPercent = %d, want 95 (explicit in default.yaml)", cc.Thresholds.HardPercent)
	}
	if cc.Strategies.Hard != "summarize" && cc.Strategy != "summarize" {
		t.Logf("hard strategy sources: layer=%q legacy=%q", cc.Strategies.Hard, cc.Strategy)
	}
	if cc.OnErrorStrategy != "hybrid" {
		t.Errorf("OnErrorStrategy = %q, want hybrid (embedded default)", cc.OnErrorStrategy)
	}
	if cc.Strategy != "" {
		t.Errorf("legacy Strategy = %q, want empty (trigger-layer strategy unset by default)", cc.Strategy)
	}
	if !cc.OnContextError {
		t.Error("OnContextError = false, want true (reactive safety net stays on)")
	}
	if cc.ThresholdPercent != 0 {
		t.Errorf("legacy ThresholdPercent = %d, want 0 (migrated to thresholds block)", cc.ThresholdPercent)
	}
}

// TestDeepMergeContextCompressionToolResultPruning verifies the pruner
// settings (CX1) merge field-wise across cascade layers: a higher layer
// setting one key does not reset the others.
func TestDeepMergeContextCompressionToolResultPruning(t *testing.T) {
	base := &Config{ContextCompression: ContextCompressionConfig{
		Enabled:           boolPtr(true),
		ToolResultPruning: ToolResultPruningSettings{ThresholdChars: 4096, HeadChars: 2048},
	}}
	override := &Config{ContextCompression: ContextCompressionConfig{
		Enabled:           boolPtr(true),
		ToolResultPruning: ToolResultPruningSettings{TailChars: 256},
	}}
	base.DeepMerge(override)
	got := base.ContextCompression.ToolResultPruning
	if got.ThresholdChars != 4096 || got.HeadChars != 2048 || got.TailChars != 256 {
		t.Errorf("ToolResultPruning = %+v, want {4096 2048 256} (field-wise merge)", got)
	}
}

// TestConfigValidateToolResultPruning covers the CX1 budget validation:
// negative values are rejected and head + marker + tail must fit within the
// threshold (dsh compaction-tool-result-pruner config rule; the marker is 39
// runes).
func TestConfigValidateToolResultPruning(t *testing.T) {
	tests := []struct {
		name    string
		pruning ToolResultPruningSettings
		wantErr string // substring; empty = valid
	}{
		{name: "zero inherits defaults", pruning: ToolResultPruningSettings{}},
		{
			name:    "valid custom budgets",
			pruning: ToolResultPruningSettings{ThresholdChars: 4096, HeadChars: 2048, TailChars: 512},
		},
		{
			name:    "negative threshold rejected",
			pruning: ToolResultPruningSettings{ThresholdChars: -1},
			wantErr: "must be non-negative",
		},
		{
			name:    "negative head rejected",
			pruning: ToolResultPruningSettings{HeadChars: -5},
			wantErr: "must be non-negative",
		},
		{
			name:    "head+marker+tail over threshold rejected",
			pruning: ToolResultPruningSettings{ThresholdChars: 100, HeadChars: 90, TailChars: 50},
			wantErr: "must be at most threshold_chars",
		},
		{
			// 4096 + 39 + 1024 = 5159 ≤ 8192 (the default threshold).
			name:    "explicit head/tail fit the default threshold",
			pruning: ToolResultPruningSettings{HeadChars: 4096, TailChars: 1024},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{ContextCompression: ContextCompressionConfig{
				Enabled:           boolPtr(true),
				ToolResultPruning: tt.pruning,
			}}
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

// TestDeepMergeContextCompressionFreshWindow covers the field-wise merge of
// the fresh_window settings (2b.3): a higher layer setting one key does not
// reset the others, and the tri-state Enabled only overrides when set.
func TestDeepMergeContextCompressionFreshWindow(t *testing.T) {
	base := &Config{ContextCompression: ContextCompressionConfig{
		Enabled:     boolPtr(true),
		FreshWindow: FreshWindowSettings{Enabled: boolPtr(true), PreserveRecentTurns: 3},
	}}
	override := &Config{ContextCompression: ContextCompressionConfig{
		Enabled:     boolPtr(true),
		FreshWindow: FreshWindowSettings{PreserveRecentTurns: 1},
	}}
	base.DeepMerge(override)
	got := base.ContextCompression.FreshWindow
	if got.Enabled == nil || !*got.Enabled {
		t.Errorf("FreshWindow.Enabled = %v, want preserved true (tri-state merge)", got.Enabled)
	}
	if got.PreserveRecentTurns != 1 {
		t.Errorf("FreshWindow.PreserveRecentTurns = %d, want 1 (higher layer wins)", got.PreserveRecentTurns)
	}
	// An explicit false in a higher layer disables the gate (reversible).
	off := &Config{ContextCompression: ContextCompressionConfig{
		FreshWindow: FreshWindowSettings{Enabled: boolPtr(false)},
	}}
	base.DeepMerge(off)
	if base.ContextCompression.FreshWindow.Enabled == nil || *base.ContextCompression.FreshWindow.Enabled {
		t.Errorf("FreshWindow.Enabled = %v, want explicit false to win (reversible gate)", base.ContextCompression.FreshWindow.Enabled)
	}
}

// TestConfigValidateFreshWindow covers the 2b.3 validation: fresh_window is a
// known strategy on every slot, and the preservation tail must be
// non-negative.
func TestConfigValidateFreshWindow(t *testing.T) {
	tests := []struct {
		name    string
		cfg     ContextCompressionConfig
		wantErr string // substring; empty = valid
	}{
		{
			name: "zero fresh_window block valid",
			cfg:  ContextCompressionConfig{Enabled: boolPtr(true)},
		},
		{
			name: "fresh_window accepted on hard layer",
			cfg: ContextCompressionConfig{
				Enabled:    boolPtr(true),
				Strategies: CompressionLayerStrategiesConfig{Hard: AgenticCompressionFreshWindow},
			},
		},
		{
			name: "fresh_window accepted on soft layer",
			cfg: ContextCompressionConfig{
				Enabled:    boolPtr(true),
				Strategies: CompressionLayerStrategiesConfig{Soft: AgenticCompressionFreshWindow},
			},
		},
		{
			name: "fresh_window accepted as legacy strategy",
			cfg: ContextCompressionConfig{
				Enabled:  boolPtr(true),
				Strategy: AgenticCompressionFreshWindow,
			},
		},
		{
			name: "fresh_window accepted as on_error_strategy",
			cfg: ContextCompressionConfig{
				Enabled:         boolPtr(true),
				OnErrorStrategy: AgenticCompressionFreshWindow,
			},
		},
		{
			name: "fresh_window accepted per-model",
			cfg: ContextCompressionConfig{
				Enabled:  boolPtr(true),
				PerModel: map[string]ModelCompressionOverride{"qwen": {Strategy: AgenticCompressionFreshWindow}},
			},
		},
		{
			name: "valid fresh_window settings",
			cfg: ContextCompressionConfig{
				Enabled:     boolPtr(true),
				FreshWindow: FreshWindowSettings{Enabled: boolPtr(true), PreserveRecentTurns: 2},
			},
		},
		{
			name: "negative preserve rejected",
			cfg: ContextCompressionConfig{
				Enabled:     boolPtr(true),
				FreshWindow: FreshWindowSettings{PreserveRecentTurns: -1},
			},
			wantErr: "fresh_window.preserve_recent_turns: must be >= 0",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				ContextCompression: tt.cfg,
				Models:             []ModelConfig{{ID: "qwen", ProviderID: "p", Model: "qwen3"}},
			}
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
