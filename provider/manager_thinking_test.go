// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/models"
)

// opencodeTestConfig builds a config whose active model is the manually-added
// x-preview-f-free on the opencode (zen) endpoint — the exact shape from the
// reported HTTP 400 export.
func opencodeTestConfig(mCfg config.ModelConfig) *config.Config {
	mCfg.ID = "x-preview-f-free"
	mCfg.ProviderID = "opencode"
	mCfg.Model = "x-preview-f-free"
	return &config.Config{
		ActiveProvider: "opencode",
		ActiveModel:    "x-preview-f-free",
		Providers: []config.ProviderConfig{
			{ID: "opencode", Provider: "opencode", Endpoint: "https://opencode.ai/zen/v1", APIKey: "k"},
		},
		Models: []config.ModelConfig{mCfg},
	}
}

// Part A regression: the configured thinking level must reach the wire. Before
// the fix, BuildStreamOptions never set StreamOptions.Reasoning, so the
// protocol layer fell back to "medium" — which x-preview-f-free rejects with
// HTTP 400 (it accepts only low/high/max).
func TestBuildStreamOptions_PlumbsThinkingLevel(t *testing.T) {
	pm := NewProviderManager(opencodeTestConfig(config.ModelConfig{ThinkingLevel: "high"}))
	opts := pm.BuildStreamOptions()
	if opts.Reasoning != agenticprovider.ThinkingHigh {
		t.Errorf("StreamOptions.Reasoning = %q, want %q (configured thinking_level must reach the wire)", opts.Reasoning, agenticprovider.ThinkingHigh)
	}
}

// TestResolveActiveModel_CatJamFreeOnGoGateway pins the fix for exports
// goa-export-20261013-220109 / -222008: cat-jam-free on opencode-go failed
// every turn with HTTP 400 "[unsupported_parameter] Unrecognized request
// argument supplied: thinking." The zen gateways are OpenAI-compatible
// surfaces (models.dev npm "@ai-sdk/openai-compatible" → the reference
// client's openaiChat protocol), so the resolved profile must describe an
// effort-only reasoning body while keeping the reasoning_content echo the
// upstream requires.
func TestResolveActiveModel_CatJamFreeOnGoGateway(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "opencode-go",
		ActiveModel:    "cat-jam-free",
		Providers: []config.ProviderConfig{
			{ID: "opencode-go", Provider: "opencode-go", Endpoint: "https://opencode.ai/zen/go/v1", APIKey: "k"},
		},
		Models: []config.ModelConfig{
			{ID: "cat-jam-free", ProviderID: "opencode-go", Model: "cat-jam-free"},
		},
	}
	pm := NewProviderManager(cfg)
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}

	profile := agenticprovider.ResolveProfile(mdl)
	if got := profile.Compat.ThinkingFormat; got != "openai" {
		t.Errorf("profile thinking format = %q, want %q: zen upstreams reject the thinking object", got, "openai")
	}
	if !profile.Compat.RequiresReasoningContentOnAssistantMessages {
		t.Error("reasoning_content echo requirement lost: cat-jam-free 400s with " +
			"missing_required_parameter messages[1].reasoning_content")
	}
}

// When no thinking level is configured, Reasoning stays empty (the protocol
// layer applies the profile/fallback default).
func TestBuildStreamOptions_NoThinkingLevelLeavesReasoningEmpty(t *testing.T) {
	pm := NewProviderManager(opencodeTestConfig(config.ModelConfig{}))
	opts := pm.BuildStreamOptions()
	if opts.Reasoning != "" {
		t.Errorf("StreamOptions.Reasoning = %q, want empty when no thinking_level configured", opts.Reasoning)
	}
}

// Part B regression: the direct per-model native map must reach the resolved
// model's ThinkingLevelMap so resolveThinkingLevel translates the canonical
// level (xhigh) into the provider-native value (max) the model accepts.
func TestResolveActiveModel_NativeThinkingLevelMap(t *testing.T) {
	pm := NewProviderManager(opencodeTestConfig(config.ModelConfig{
		ThinkingLevel: "xhigh",
		ThinkingLevelNativeMap: map[string]string{
			"xhigh": "max",
			"high":  "high",
			"low":   "low",
		},
	}))
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	if !mdl.Reasoning {
		t.Error("ResolveActiveModel Reasoning = false, want true for a thinking model")
	}
	got := mdl.ThinkingLevelMap[agenticprovider.ThinkingXHigh]
	if got != "max" {
		t.Errorf("ThinkingLevelMap[xhigh] = %q, want %q (native map must translate the level)", got, "max")
	}
}

// The config contributes no level map when it omits thinking_level_native_map:
// whatever map the resolved model carries is the catalog's own (models.dev
// effort values, then curated overrides), not an injected identity map.
func TestResolveActiveModel_NoNativeMapKeepsCatalogMapOnly(t *testing.T) {
	pm := NewProviderManager(opencodeTestConfig(config.ModelConfig{ThinkingLevel: "high"}))
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}

	catalog := models.GetModelForProvider(mdl.Provider, mdl.ID)
	if catalog == nil {
		t.Fatalf("catalog has no entry for %s/%s", mdl.Provider, mdl.ID)
	}
	if len(catalog.ThinkingLevelMap) == 0 {
		t.Fatal("precondition: the catalog entry carries a thinking level map")
	}
	if len(mdl.ThinkingLevelMap) != len(catalog.ThinkingLevelMap) {
		t.Errorf("ThinkingLevelMap = %v, want the catalog's %v with nothing added",
			mdl.ThinkingLevelMap, catalog.ThinkingLevelMap)
	}
	for level, want := range catalog.ThinkingLevelMap {
		if got := mdl.ThinkingLevelMap[level]; got != want {
			t.Errorf("ThinkingLevelMap[%q] = %q, want the catalog's %q", level, got, want)
		}
	}
}

// The config native map must merge per key over a registry model's built-in
// map instead of replacing it, so levels the user did not remap stay
// resolvable for clamping (o1's registry map is off/low/medium/high).
func TestResolveActiveModel_NativeMapMergesOverRegistryMapPerKey(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "openai",
		ActiveModel:    "o1",
		Providers: []config.ProviderConfig{
			{ID: "openai", Provider: "openai", Endpoint: "https://api.openai.com/v1", APIKey: "k"},
		},
		Models: []config.ModelConfig{
			{
				ID:         "o1",
				ProviderID: "openai",
				Model:      "o1",
				ThinkingLevelNativeMap: map[string]string{
					"high": "high",
				},
			},
		},
	}
	pm := NewProviderManager(cfg)
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	if got := mdl.ThinkingLevelMap[agenticprovider.ThinkingHigh]; got != "high" {
		t.Errorf("ThinkingLevelMap[high] = %q, want the config remap value %q", got, "high")
	}
	if got := mdl.ThinkingLevelMap[agenticprovider.ThinkingLow]; got != "low" {
		t.Errorf("ThinkingLevelMap[low] = %q, want the registry mapping %q kept", got, "low")
	}
	if got := mdl.ThinkingLevelMap[agenticprovider.ThinkingMedium]; got != "medium" {
		t.Errorf("ThinkingLevelMap[medium] = %q, want the registry mapping %q kept", got, "medium")
	}
}
