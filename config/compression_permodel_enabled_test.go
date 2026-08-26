// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"testing"
)

// TestMergeCompressionPerModel_FullFieldFidelity pins the cascade-merge defect
// from bugs.md (2026-08-26): overlaying a per-model entry from a higher
// cascade layer must be faithful for EVERY field — Enabled (tri-state
// pointer), Strategies and CacheGate were silently dropped.
func TestMergeCompressionPerModel_FullFieldFidelity(t *testing.T) {
	base := &Config{ContextCompression: ContextCompressionConfig{
		Enabled: boolPtr(true),
		PerModel: map[string]ModelCompressionOverride{
			"m1": {
				MaxTokens: 24576,
				Strategy:  "hybrid",
				Thresholds: CompressionThresholdsConfig{
					HardPercent: 20,
				},
				Strategies: CompressionLayerStrategiesConfig{Hard: "summarize"},
				CacheGate:  "off",
			},
		},
	}}
	override := &Config{ContextCompression: ContextCompressionConfig{
		PerModel: map[string]ModelCompressionOverride{
			// Higher layer tunes ONLY the hard percent; every other field must
			// survive — including the pointer and string fields the old merge
			// dropped.
			"m1": {Thresholds: CompressionThresholdsConfig{HardPercent: 30}},
		},
	}}
	base.DeepMerge(override)

	o := base.ContextCompression.PerModel["m1"]
	if o.Thresholds.HardPercent != 30 {
		t.Errorf("HardPercent = %d, want 30 (overridden)", o.Thresholds.HardPercent)
	}
	if o.MaxTokens != 24576 {
		t.Errorf("MaxTokens = %d, want 24576 (preserved)", o.MaxTokens)
	}
	if o.Strategy != "hybrid" {
		t.Errorf("Strategy = %q, want hybrid (preserved)", o.Strategy)
	}
	if o.Strategies.Hard != "summarize" {
		t.Errorf("Strategies.Hard = %q, want summarize (preserved across layer merge)", o.Strategies.Hard)
	}
	if o.CacheGate != "off" {
		t.Errorf("CacheGate = %q, want off (preserved across layer merge)", o.CacheGate)
	}
}

// TestMergeCompressionPerModel_EnabledTriState verifies the per-model enabled
// flag merges only when the higher layer STATES it (non-nil pointer), so a
// layer that never mentions enabled cannot clobber a lower layer's choice.
func TestMergeCompressionPerModel_EnabledTriState(t *testing.T) {
	base := &Config{ContextCompression: ContextCompressionConfig{
		PerModel: map[string]ModelCompressionOverride{
			"m1": {Enabled: boolPtr(false), Thresholds: CompressionThresholdsConfig{HardPercent: 20}},
		},
	}}
	// Higher layer restating only thresholds must NOT clear the explicit false.
	override := &Config{ContextCompression: ContextCompressionConfig{
		PerModel: map[string]ModelCompressionOverride{
			"m1": {Thresholds: CompressionThresholdsConfig{HardPercent: 25}},
		},
	}}
	base.DeepMerge(override)
	o := base.ContextCompression.PerModel["m1"]
	if o.Enabled == nil || *o.Enabled != false {
		t.Errorf("Enabled = %v, want explicit false (preserved)", o.Enabled)
	}
	if o.Thresholds.HardPercent != 25 {
		t.Errorf("HardPercent = %d, want 25 (overridden)", o.Thresholds.HardPercent)
	}

	// A higher layer stating enabled:true overrides the lower false.
	override2 := &Config{ContextCompression: ContextCompressionConfig{
		PerModel: map[string]ModelCompressionOverride{
			"m1": {Enabled: boolPtr(true)},
		},
	}}
	base.DeepMerge(override2)
	o = base.ContextCompression.PerModel["m1"]
	if o.Enabled == nil || *o.Enabled != true {
		t.Errorf("Enabled = %v, want explicit true (overridden)", o.Enabled)
	}
}

// TestCompressionEnabledForModel covers the resolver matrix (bugs.md
// 2026-08-26): explicit per-model enabled wins both ways; a per-model entry
// stating a threshold implicitly activates compression for that model when
// the global flag is off; otherwise the global flag governs.
func TestCompressionEnabledForModel(t *testing.T) {
	tests := []struct {
		name   string
		global *bool // nil = default on
		ov     ModelCompressionOverride
		hasOv  bool
		want   bool
	}{
		{name: "default on, no override", global: nil, hasOv: false, want: true},
		{name: "global on, override without enabled", global: boolPtr(true), hasOv: true, ov: ModelCompressionOverride{Thresholds: CompressionThresholdsConfig{HardPercent: 20}}, want: true},
		{name: "global off, no override", global: boolPtr(false), hasOv: false, want: false},
		{name: "global off, override without thresholds (inert)", global: boolPtr(false), hasOv: true, ov: ModelCompressionOverride{MaxTokens: 8192}, want: false},
		// The frigolite shape: a per-model hard ceiling under a blanket
		// enabled:false must still activate compression for that model.
		{name: "global off, override states hard ceiling (implicit opt-in)", global: boolPtr(false), hasOv: true, ov: ModelCompressionOverride{Thresholds: CompressionThresholdsConfig{HardPercent: 20}}, want: true},
		{name: "global off, override states soft ceiling (implicit opt-in)", global: boolPtr(false), hasOv: true, ov: ModelCompressionOverride{Thresholds: CompressionThresholdsConfig{SoftPercent: 40}}, want: true},
		{name: "global off, override states legacy threshold_percent", global: boolPtr(false), hasOv: true, ov: ModelCompressionOverride{ThresholdPercent: 60}, want: true},
		{name: "global off, override explicit enabled:true", global: boolPtr(false), hasOv: true, ov: ModelCompressionOverride{Enabled: boolPtr(true)}, want: true},
		{name: "global on, override explicit enabled:false", global: boolPtr(true), hasOv: true, ov: ModelCompressionOverride{Enabled: boolPtr(false), Thresholds: CompressionThresholdsConfig{HardPercent: 20}}, want: false},
		{name: "global off, override explicit enabled:false beats stated threshold", global: boolPtr(false), hasOv: true, ov: ModelCompressionOverride{Enabled: boolPtr(false), Thresholds: CompressionThresholdsConfig{HardPercent: 20}}, want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{ContextCompression: ContextCompressionConfig{Enabled: tt.global}}
			if tt.hasOv {
				cfg.ContextCompression.PerModel = map[string]ModelCompressionOverride{"m1": tt.ov}
			}
			if got := cfg.ContextCompression.CompressionEnabledForModel("m1"); got != tt.want {
				t.Errorf("CompressionEnabledForModel(m1) = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestCompressionEnabledForModel_UnknownModel: a model with no override entry
// always follows the global flag.
func TestCompressionEnabledForModel_UnknownModel(t *testing.T) {
	cfg := &Config{ContextCompression: ContextCompressionConfig{
		Enabled: boolPtr(false),
		PerModel: map[string]ModelCompressionOverride{
			"other": {Thresholds: CompressionThresholdsConfig{HardPercent: 20}},
		},
	}}
	if cfg.ContextCompression.CompressionEnabledForModel("missing") {
		t.Error("unknown model must follow the global flag (false)")
	}
	if cfg.ContextCompression.CompressionEnabledForModel("") {
		t.Error("empty model ID must follow the global flag (false)")
	}
}
