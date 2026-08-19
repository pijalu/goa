// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"testing"

	"gopkg.in/yaml.v3"
)

// TestFeaturesRemoteCompaction_Parse verifies the gate parses from YAML and
// defaults off when the features block is absent.
func TestFeaturesRemoteCompaction_Parse(t *testing.T) {
	// Absent -> default off.
	var off Config
	if err := yaml.Unmarshal([]byte("active_model: m\n"), &off); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if off.Features.RemoteCompactionEnabled() {
		t.Error("absent features.remote_compaction must default to off")
	}

	// Explicit true.
	var on Config
	if err := yaml.Unmarshal([]byte("features:\n  remote_compaction: true\n"), &on); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if !on.Features.RemoteCompactionEnabled() {
		t.Error("features.remote_compaction: true must resolve on")
	}

	// Explicit false (reversible).
	var explicit Config
	if err := yaml.Unmarshal([]byte("features:\n  remote_compaction: false\n"), &explicit); err != nil {
		t.Fatalf("Unmarshal failed: %v", err)
	}
	if explicit.Features.RemoteCompactionEnabled() {
		t.Error("features.remote_compaction: false must resolve off")
	}
}

// TestFeaturesRemoteCompaction_DeepMerge verifies the gate merges across
// cascade layers with tri-state semantics: a higher layer may turn it on, an
// explicit false higher up reverses a lower true, and an unset higher layer
// inherits the lower value.
func TestFeaturesRemoteCompaction_DeepMerge(t *testing.T) {
	// Lower off + higher on -> on.
	base := &Config{}
	override := &Config{Features: FeaturesConfig{RemoteCompaction: boolPtr(true)}}
	base.DeepMerge(override)
	if !base.Features.RemoteCompactionEnabled() {
		t.Error("higher-layer true must enable the gate")
	}

	// Lower on + higher explicit false -> off (reversible).
	base2 := &Config{Features: FeaturesConfig{RemoteCompaction: boolPtr(true)}}
	base2.DeepMerge(&Config{Features: FeaturesConfig{RemoteCompaction: boolPtr(false)}})
	if base2.Features.RemoteCompactionEnabled() {
		t.Error("higher-layer explicit false must reverse a lower true")
	}

	// Lower on + higher unset -> inherit on.
	base3 := &Config{Features: FeaturesConfig{RemoteCompaction: boolPtr(true)}}
	base3.DeepMerge(&Config{})
	if !base3.Features.RemoteCompactionEnabled() {
		t.Error("unset higher layer must inherit the lower-layer value")
	}
}

// TestFeaturesRemoteCompaction_Validate verifies a config carrying the gate
// (on or off) passes validation — the gate is a plain opt-in flag with no
// invalid states.
func TestFeaturesRemoteCompaction_Validate(t *testing.T) {
	for _, v := range []*bool{nil, boolPtr(true), boolPtr(false)} {
		cfg := &Config{Features: FeaturesConfig{RemoteCompaction: v}}
		if err := cfg.Validate(); err != nil {
			t.Errorf("Validate() with remote_compaction=%v returned error: %v", v, err)
		}
	}
}

// TestFeaturesRemoteCompaction_DeepCopy verifies the tri-state pointer is
// deep-copied (mutating the copy does not alias the source).
func TestFeaturesRemoteCompaction_DeepCopy(t *testing.T) {
	src := &Config{Features: FeaturesConfig{RemoteCompaction: boolPtr(true)}}
	cp := src.DeepCopy()
	if !cp.Features.RemoteCompactionEnabled() {
		t.Fatal("deep copy must preserve the gate value")
	}
	*cp.Features.RemoteCompaction = false
	if !src.Features.RemoteCompactionEnabled() {
		t.Fatal("deep copy must not alias the source pointer")
	}
}
