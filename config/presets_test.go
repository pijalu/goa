// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package config

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// TestPresetProviders_ContainsAllPresets verifies PresetProviders returns the expected set.
func TestPresetProviders_ContainsAllPresets(t *testing.T) {
	presets := PresetProviders()
	if len(presets) < 10 {
		t.Fatalf("PresetProviders() returned %d presets, want >= 10", len(presets))
	}

	// Check each preset has non-empty required fields. DefaultModel is NOT
	// required: it is truly optional and only curated for providers with a
	// known suggested model (most catalog entries carry none; the setup wizard
	// falls back to a default and /model:add resolves the live model list).
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
		{"zai", "Z.ai Coding", "glm-5.2", true},
		{"zai-api", "Z.ai", "glm-5.2", true},
		{"poolside", "Poolside", "poolside-default", true},
		// Surfaced from the catalog: OpenAI-compatible and native API cloud
		// providers with a wizard-addable base URL. No curated default model.
		{"anthropic", "Anthropic", "", true},
		{"google", "Google", "", true},
		{"mistral", "Mistral", "", true},
		{"groq", "Groq", "", true},
		{"xai", "xAI", "", true},
		{"together", "Together", "", true},
		{"fireworks", "Fireworks", "", true},
		{"perplexity", "Perplexity", "", true},
		{"github", "GitHub Copilot", "", true},
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

// TestPresetProviders_ZaiEndpoints pins the z.ai preset endpoints: "zai" is
// the GLM Coding Plan (subscription/quota, mirroring pi's default z.ai
// provider), "zai-api" is the pay-per-token general API. Swapping these
// would silently bill subscription users or break quota tracking.
func TestPresetProviders_ZaiEndpoints(t *testing.T) {
	zai := FindPreset("zai")
	if zai == nil {
		t.Fatal("FindPreset(zai) = nil")
	}
	if zai.Endpoint != "https://api.z.ai/api/coding/paas/v4" {
		t.Errorf("zai Endpoint = %q, want coding endpoint", zai.Endpoint)
	}
	if zai.Provider != AgenticProviderZai {
		t.Errorf("zai Provider = %q, want %q", zai.Provider, AgenticProviderZai)
	}

	api := FindPreset("zai-api")
	if api == nil {
		t.Fatal("FindPreset(zai-api) = nil")
	}
	if api.Endpoint != "https://api.z.ai/api/paas/v4" {
		t.Errorf("zai-api Endpoint = %q, want general endpoint", api.Endpoint)
	}
	if api.Provider != AgenticProviderZaiApi {
		t.Errorf("zai-api Provider = %q, want %q", api.Provider, AgenticProviderZaiApi)
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
// unexpectedly. Catalog order is authoritative; this list is the exact set of
// catalog entries that carry a wizard-addable base URL (see
// wizardPresetAddable). Adding a provider to the catalog with a base URL must
// also extend this list (TestPresetProviders_CoverCatalogProviders guards it).
func TestPresetProviders_StableOrder(t *testing.T) {
	presets := PresetProviders()
	expected := []string{
		"openai", "lmstudio", "ollama", "openrouter",
		"opencode", "opencode-go", "deepseek", "kimi", "kimi-code",
		"zai", "zai-api", "poolside",
		"anthropic", "google", "mistral",
		"groq", "xai", "together", "fireworks", "perplexity", "github",
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

// TestPresetProviders_CoverCatalogProviders verifies the core invariant behind
// this fix: EVERY catalog provider with a wizard-addable base URL MUST be a
// preset. This is what prevents a provider added to the catalog (e.g. groq,
// xai, together) from silently missing from the /provider add picker.
func TestPresetProviders_CoverCatalogProviders(t *testing.T) {
	presets := PresetProviders()
	got := make(map[string]bool, len(presets))
	for _, p := range presets {
		got[p.ID] = true
	}
	for _, d := range schema.ProviderCatalog() {
		if !wizardPresetAddable(&d) {
			continue
		}
		if !got[d.ID] {
			t.Errorf("catalog provider %q (base URL %q) missing from PresetProviders", d.ID, d.BaseURL)
		}
	}
}

// TestAllProviderPresets_CoversModelsDev verifies the "add provider" pickers
// surface every models.dev provider that has no catalog preset (e.g. tensorx),
// and do not duplicate catalog presets.
func TestAllProviderPresets_CoversModelsDev(t *testing.T) {
	all := AllProviderPresets()
	byID := make(map[string]ProviderPreset, len(all))
	for _, p := range all {
		if _, dup := byID[p.ID]; dup {
			t.Errorf("AllProviderPresets has duplicate ID %q", p.ID)
		}
		byID[p.ID] = p
	}
	// The concrete regression: tensorx (a models.dev-only provider) is addable.
	p, ok := byID["tensorx"]
	if !ok {
		t.Fatal("tensorx missing from AllProviderPresets")
	}
	if p.Endpoint == "" {
		t.Errorf("tensorx preset Endpoint = %q, want base URL", p.Endpoint)
	}
	if !p.NeedsAPIKey {
		t.Error("tensorx preset NeedsAPIKey = false, want true (cloud provider)")
	}
	if p.Provider != "tensorx" {
		t.Errorf("tensorx preset Provider = %q, want %q", p.Provider, "tensorx")
	}

	// FindPreset must resolve models.dev providers too (picker callback + manager identity).
	if f := FindPreset("tensorx"); f == nil {
		t.Error("FindPreset(tensorx) = nil, want models.dev preset")
	}

	// Catalog presets must appear exactly once in the full addable set.
	for _, cp := range PresetProviders() {
		if _, ok := byID[cp.ID]; !ok {
			t.Errorf("catalog preset %q missing from AllProviderPresets", cp.ID)
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
