// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"

	"github.com/pijalu/goa/config"
)

// zaiContextWindowTestConfig builds a minimal active z.ai provider config for
// the given wire model name (an id with no ModelConfig context_window).
func zaiContextWindowTestConfig(model string) *config.Config {
	return &config.Config{
		ActiveProvider: "zai",
		ActiveModel:    "m",
		Providers: []config.ProviderConfig{
			{ID: "zai", Name: "Z.ai Coding", Provider: "zai", Endpoint: "https://api.z.ai/api/coding/paas/v4", APIKey: "k"},
		},
		Models: []config.ModelConfig{
			{ID: "m", ProviderID: "zai", Model: model},
		},
	}
}

// TestResolveActiveModel_ZaiUnknownModelGetsWindow is the bugs.md #3
// regression: z.ai accepts an over-window request silently, so a model id the
// catalog does not know must still resolve to a real context window — with 0
// every token-count threshold is inert and compression waits forever for a
// context-length error that never arrives.
func TestResolveActiveModel_ZaiUnknownModelGetsWindow(t *testing.T) {
	pm := NewProviderManager(zaiContextWindowTestConfig("glm-99-not-in-the-snapshot"))

	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	if mdl.ContextWindow <= 0 {
		t.Errorf("ContextWindow = %d, want a positive bound for an unknown z.ai model", mdl.ContextWindow)
	}
	if mdl.ID != "glm-99-not-in-the-snapshot" {
		t.Errorf("ID = %q, want the configured model name passed through verbatim", mdl.ID)
	}
}

// TestResolveActiveModel_ZaiKnownModelKeepsRegistryWindow pins that the floor
// never overrides a model the catalog knows: GLM-5.2 is a 1M-window model.
func TestResolveActiveModel_ZaiKnownModelKeepsRegistryWindow(t *testing.T) {
	pm := NewProviderManager(zaiContextWindowTestConfig("glm-5.2"))

	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	if mdl.ContextWindow != 1000000 {
		t.Errorf("ContextWindow = %d, want 1000000 (registry value for glm-5.2)", mdl.ContextWindow)
	}
}

// TestResolveActiveModel_UserContextWindowWins keeps the override contract: an
// explicit model-config context_window (e.g. a z.ai gateway serving a smaller
// window than the public API) is the user's statement about THIS deployment
// and must not be replaced by the provider floor.
func TestResolveActiveModel_UserContextWindowWins(t *testing.T) {
	cfg := zaiContextWindowTestConfig("glm-99-not-in-the-snapshot")
	cfg.Models[0].ContextWindow = 32768
	pm := NewProviderManager(cfg)

	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	if mdl.ContextWindow != 32768 {
		t.Errorf("ContextWindow = %d, want 32768 (user override wins over the floor)", mdl.ContextWindow)
	}
}

// TestResolveActiveModel_NoFloorProviderStaysUnknown keeps the floor scoped to
// the providers that declare one: an unknown model on a provider with no
// declared floor still resolves to 0 (no bound, the pre-existing contract).
func TestResolveActiveModel_NoFloorProviderStaysUnknown(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "ollama",
		ActiveModel:    "m",
		Providers: []config.ProviderConfig{
			{ID: "ollama", Name: "Ollama", Provider: "ollama", Endpoint: "http://localhost:11434/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "m", ProviderID: "ollama", Model: "some-unlisted-local-model"},
		},
	}
	pm := NewProviderManager(cfg)

	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	if mdl.ContextWindow != 0 {
		t.Errorf("ContextWindow = %d, want 0 (no floor declared for ollama)", mdl.ContextWindow)
	}
}
