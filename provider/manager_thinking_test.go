// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
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

// Without a native map, the resolved model carries none (the variant profile's
// own map, if any, applies instead via ResolveProfile's migration bridge).
func TestResolveActiveModel_NoNativeMapLeavesModelMapEmpty(t *testing.T) {
	pm := NewProviderManager(opencodeTestConfig(config.ModelConfig{ThinkingLevel: "high"}))
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	if len(mdl.ThinkingLevelMap) != 0 {
		t.Errorf("ThinkingLevelMap = %v, want empty when no thinking_level_native_map configured", mdl.ThinkingLevelMap)
	}
}
