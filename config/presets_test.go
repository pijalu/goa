// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"testing"
)

// TestPresetProviders_ContainsAllPresets verifies PresetProviders returns the expected set.
func TestPresetProviders_ContainsAllPresets(t *testing.T) {
	presets := PresetProviders()
	if len(presets) < 9 {
		t.Fatalf("PresetProviders() returned %d presets, want >= 9", len(presets))
	}

	// Check each preset has non-empty required fields
	for _, p := range presets {
		if p.ID == "" {
			t.Errorf("Preset %+v has empty ID", p)
		}
		if p.Name == "" {
			t.Errorf("Preset %q has empty Name", p.ID)
		}
		if p.Endpoint == "" {
			t.Errorf("Preset %q has empty Endpoint", p.ID)
		}
		if p.DefaultModel == "" {
			t.Errorf("Preset %q has empty DefaultModel", p.ID)
		}
	}

	for _, tt := range presetExpectations() {
		assertPreset(t, tt)
	}
}

type presetExpectation struct {
	id         string
	wantName   string
	wantModel  string
	wantAPIKey bool
}

func presetExpectations() []presetExpectation {
	return []presetExpectation{
		{"openai", "OpenAI", "gpt-4o", true},
		{"lmstudio", "LM Studio", "local-model", false},
		{"ollama", "Ollama", "qwen/qwen3.5-9b", false},
		{"openrouter", "OpenRouter", "openrouter/free", true},
		{"opencode", "OpenCode Zen", "deepseek-v4-flash", true},
		{"opencode-go", "OpenCode Go", "deepseek-v4-flash", true},
		{"deepseek", "DeepSeek", "deepseek-v4-flash", true},
		{"kimi", "Moonshot", "kimi-k2.6", true},
		{"kimi-code", "Kimi Code", "kimi-for-coding", true},
	}
}

func assertPreset(t *testing.T, tt presetExpectation) {
	t.Helper()
	p := FindPreset(tt.id)
	if p == nil {
		t.Errorf("FindPreset(%q) returned nil, want preset", tt.id)
		return
	}
	if p.Name != tt.wantName {
		t.Errorf("Preset %q Name = %q, want %q", tt.id, p.Name, tt.wantName)
	}
	if p.DefaultModel != tt.wantModel {
		t.Errorf("Preset %q DefaultModel = %q, want %q", tt.id, p.DefaultModel, tt.wantModel)
	}
	if p.NeedsAPIKey != tt.wantAPIKey {
		t.Errorf("Preset %q NeedsAPIKey = %v, want %v", tt.id, p.NeedsAPIKey, tt.wantAPIKey)
	}
}

// TestFindPreset_Missing verifies FindPreset returns nil for unknown IDs.
func TestFindPreset_Missing(t *testing.T) {
	if p := FindPreset("nonexistent"); p != nil {
		t.Errorf("FindPreset('nonexistent') = %+v, want nil", p)
	}
}

// TestIsPresetID verifies IsPresetID checks correctly.
func TestIsPresetID(t *testing.T) {
	if !IsPresetID("openai") {
		t.Error("IsPresetID('openai') = false, want true")
	}
	if !IsPresetID("deepseek") {
		t.Error("IsPresetID('deepseek') = false, want true")
	}
	if !IsPresetID("kimi-code") {
		t.Error("IsPresetID('kimi-code') = false, want true")
	}
	if IsPresetID("") {
		t.Error("IsPresetID('') = true, want false")
	}
	if IsPresetID("made-up-provider") {
		t.Error("IsPresetID('made-up-provider') = true, want false")
	}
}

// TestPresetProviders_StableOrder verifies the preset order doesn't change
// unexpectedly, which would renumber wizard options.
func TestPresetProviders_StableOrder(t *testing.T) {
	presets := PresetProviders()
	expected := []string{
		"openai", "lmstudio", "ollama", "openrouter",
		"opencode", "opencode-go", "deepseek", "kimi", "kimi-code",
	}
	if len(presets) != len(expected) {
		t.Fatalf("PresetProviders() = %d presets, want %d", len(presets), len(expected))
	}
	for i, p := range presets {
		if p.ID != expected[i] {
			t.Errorf("Preset[%d].ID = %q, want %q (preset order changed)", i, p.ID, expected[i])
		}
	}
}

// TestPresetProviders_HaveAgenticIdentity verifies every preset maps to a
// known agentic provider and API so ResolveActiveModel can set compat flags.
func TestPresetProviders_HaveAgenticIdentity(t *testing.T) {
	for _, p := range PresetProviders() {
		if p.Provider == "" {
			t.Errorf("preset %q has no Provider", p.ID)
		}
		if !IsValidAgenticProvider(p.Provider) {
			t.Errorf("preset %q has unknown Provider %q", p.ID, p.Provider)
		}
		if p.API == "" {
			t.Errorf("preset %q has no API", p.ID)
		}
		if !IsValidAgenticAPI(p.API) {
			t.Errorf("preset %q has unknown API %q", p.ID, p.API)
		}
	}
}
