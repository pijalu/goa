// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
)

// TestProviderManagerActive verifies active provider selection.

// TestProviderManagerActive verifies active provider selection.
func TestProviderManagerActive(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "openai",
		ActiveModel:    "gpt-4o",
		Providers: []config.ProviderConfig{
			{ID: "openai", Name: "OpenAI"},
		},
		Models: []config.ModelConfig{
			{ID: "gpt-4o", ProviderID: "openai", Model: "gpt-4o"},
		},
	}
	pm := NewProviderManager(cfg)

	provider, model := pm.Active()
	if provider == nil {
		t.Fatal("Active provider should not be nil")
	}
	if provider.ID != "openai" {
		t.Errorf("Provider ID = %q, want %q", provider.ID, "openai")
	}
	if model != "gpt-4o" {
		t.Errorf("Model = %q, want %q", model, "gpt-4o")
	}
}

// TestProviderManagerActiveFallback verifies fallback to first provider.

// TestProviderManagerActiveFallback verifies fallback to first provider.
func TestProviderManagerActiveFallback(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "ollama", Name: "Ollama"},
		},
		Models: []config.ModelConfig{
			{ID: "llama3", ProviderID: "ollama", Model: "llama3"},
		},
	}
	pm := NewProviderManager(cfg)

	provider, model := pm.Active()
	if provider == nil {
		t.Fatal("Active provider should fallback to first")
	}
	if provider.ID != "ollama" {
		t.Errorf("Provider ID = %q, want %q", provider.ID, "ollama")
	}
	if model != "llama3" {
		t.Errorf("Model = %q, want %q", model, "llama3")
	}
}

// TestProviderManagerActiveEmpty verifies empty providers.

// TestProviderManagerActiveEmpty verifies empty providers.
func TestProviderManagerActiveEmpty(t *testing.T) {
	cfg := &config.Config{}
	pm := NewProviderManager(cfg)

	provider, _ := pm.Active()
	if provider != nil {
		t.Error("Active should return nil with no providers")
	}
}

// TestProviderManagerActiveUnknownDoesNotFallback verifies that an explicit
// active provider that is missing does not silently fall back to another
// provider, which would send requests (and API keys) to the wrong endpoint.

// TestProviderManagerActiveUnknownDoesNotFallback verifies that an explicit
// active provider that is missing does not silently fall back to another
// provider, which would send requests (and API keys) to the wrong endpoint.
func TestProviderManagerActiveUnknownDoesNotFallback(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "missing",
		Providers: []config.ProviderConfig{
			{ID: "other", Endpoint: "http://other.example.com/v1", APIKey: "other-key"},
		},
	}
	pm := NewProviderManager(cfg)

	provider, _ := pm.Active()
	if provider != nil {
		t.Errorf("Active should return nil for unknown provider, got %q", provider.ID)
	}
}

// TestProviderManagerSetActive verifies setting active provider.

// TestProviderManagerSetActive verifies setting active provider.
func TestProviderManagerSetActive(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "openai"},
			{ID: "anthropic"},
		},
	}
	pm := NewProviderManager(cfg)

	if err := pm.SetActive("anthropic", "claude-4"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if cfg.ActiveProvider != "anthropic" {
		t.Errorf("ActiveProvider = %q, want %q", cfg.ActiveProvider, "anthropic")
	}
	if cfg.ActiveModel != "claude-4" {
		t.Errorf("ActiveModel = %q, want %q", cfg.ActiveModel, "claude-4")
	}
}

// TestProviderManagerSetActiveUnknown verifies error for unknown provider.

// TestProviderManagerSetActiveUnknown verifies error for unknown provider.
func TestProviderManagerSetActiveUnknown(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "openai"}}}
	pm := NewProviderManager(cfg)

	err := pm.SetActive("nonexistent", "")
	if err == nil {
		t.Error("Expected error for unknown provider")
	}
}

// TestProviderManagerListModels verifies ListModels returns error without endpoint.

// TestProviderManagerListModels verifies ListModels returns error without endpoint.
func TestProviderManagerListModels(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "local", Endpoint: ""},
		},
	}
	pm := NewProviderManager(cfg)

	_, err := pm.ListModels("local")
	if err == nil {
		t.Error("ListModels without endpoint should fail")
	}
}

// TestProviderManagerListModelsUnknown verifies error for unknown provider.

// TestProviderManagerListModelsUnknown verifies error for unknown provider.
func TestProviderManagerListModelsUnknown(t *testing.T) {
	cfg := &config.Config{}
	pm := NewProviderManager(cfg)

	_, err := pm.ListModels("unknown")
	if err == nil {
		t.Error("Expected error for unknown provider")
	}
}

// TestResolveActiveModel_NoProvider verifies error when no active provider.

// TestProviderManagerSessionSelectionSurvivesHotReload reproduces the
// multi-instance provider bleed: instance A's /model switch persists
// active_provider/active_model to the shared config cascade; instance B's
// config watcher then hot-swaps a freshly loaded config via SetConfig.
// The session's explicit pick must survive that swap — the disk value is a
// startup default, not live session state (bug: B's footer AND next requests
// silently followed A's provider).
func TestProviderManagerSessionSelectionSurvivesHotReload(t *testing.T) {
	boot := &config.Config{
		ActiveProvider: "kimi-code",
		ActiveModel:    "kimi-for-coding",
		Providers: []config.ProviderConfig{
			{ID: "kimi-code", Endpoint: "https://kimi.example.com"},
			{ID: "openai", Endpoint: "https://api.openai.com/v1"},
		},
	}
	pm := NewProviderManager(boot)

	// Instance B (this process) switches provider/model at runtime.
	if err := pm.SetActive("openai", "gpt-4o"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}

	// Meanwhile instance A persisted ITS switch; the watcher hands us a
	// freshly loaded config whose Active* reflect A's choice on disk.
	fromDisk := &config.Config{
		ActiveProvider: "kimi-code",
		ActiveModel:    "kimi-for-coding",
		Providers: []config.ProviderConfig{
			{ID: "kimi-code", Endpoint: "https://kimi.example.com"},
			{ID: "openai", Endpoint: "https://api.openai.com/v1"},
		},
	}
	pm.SetConfig(fromDisk)

	pc, model := pm.Active()
	if pc == nil || pc.ID != "openai" {
		t.Fatalf("after reload Active() = %+v, want openai — session selection was clobbered by another instance's persisted switch", pc)
	}
	if model == "" || !strings.Contains(strings.ToLower(model), "gpt-4o") {
		t.Errorf("resolved model after reload = %q, want gpt-4o", model)
	}
	if got := pm.Config().ActiveProvider; got != "openai" {
		t.Errorf("Config().ActiveProvider = %q, want openai (requests would route to the other instance's provider)", got)
	}
}

// TestProviderManagerReloadAdoptsDiskDefaultWithoutExplicitPick pins the P22
// contract the other direction: a session that never switched keeps following
// manual edits of active_provider in the config files (hot-apply semantics).

// TestProviderManagerReloadAdoptsDiskDefaultWithoutExplicitPick pins the P22
// contract the other direction: a session that never switched keeps following
// manual edits of active_provider in the config files (hot-apply semantics).
func TestProviderManagerReloadAdoptsDiskDefaultWithoutExplicitPick(t *testing.T) {
	pm := NewProviderManager(&config.Config{
		ActiveProvider: "kimi-code",
		ActiveModel:    "kimi-for-coding",
		Providers:      []config.ProviderConfig{{ID: "kimi-code"}, {ID: "openai"}},
	})

	reloaded := &config.Config{
		ActiveProvider: "openai",
		ActiveModel:    "gpt-4o",
		Providers:      []config.ProviderConfig{{ID: "kimi-code"}, {ID: "openai"}},
	}
	pm.SetConfig(reloaded)

	pc, _ := pm.Active()
	if pc == nil || pc.ID != "openai" {
		t.Fatalf("without an explicit session pick, reload must adopt disk values; got %+v", pc)
	}
}

// TestProviderManagerSetActivePartialPick verifies empty halves keep their
// prior meaning: SetActive("", model) re-models without touching the provider,
// and that partial pick still survives a hot reload.

// TestProviderManagerSetActivePartialPick verifies empty halves keep their
// prior meaning: SetActive("", model) re-models without touching the provider,
// and that partial pick still survives a hot reload.
func TestProviderManagerSetActivePartialPick(t *testing.T) {
	pm := NewProviderManager(&config.Config{
		ActiveProvider: "kimi-code",
		ActiveModel:    "kimi-for-coding",
		Providers:      []config.ProviderConfig{{ID: "kimi-code"}, {ID: "openai"}},
	})
	if err := pm.SetActive("", "gpt-4o"); err != nil {
		t.Fatalf("SetActive(\"\"): %v", err)
	}

	reloaded := &config.Config{
		ActiveProvider: "openai", // other instance switched provider on disk
		ActiveModel:    "claude-x",
		Providers:      []config.ProviderConfig{{ID: "kimi-code"}, {ID: "openai"}},
	}
	pm.SetConfig(reloaded)

	cfg := pm.Config()
	if cfg.ActiveModel != "gpt-4o" {
		t.Errorf("ActiveModel = %q, want gpt-4o (explicit model pick must survive reload)", cfg.ActiveModel)
	}
	if cfg.ActiveProvider != "openai" {
		t.Errorf("ActiveProvider = %q, want openai (provider never picked explicitly → disk default stands)", cfg.ActiveProvider)
	}
}

// TestProviderManagerSetActiveUnknownLeavesSelectionUntouched verifies a
// failed switch mutates nothing (neither session selection nor config).

// TestProviderManagerSetActiveUnknownLeavesSelectionUntouched verifies a
// failed switch mutates nothing (neither session selection nor config).
func TestProviderManagerSetActiveUnknownLeavesSelectionUntouched(t *testing.T) {
	pm := NewProviderManager(&config.Config{
		ActiveProvider: "kimi-code",
		ActiveModel:    "kimi-for-coding",
		Providers:      []config.ProviderConfig{{ID: "kimi-code"}, {ID: "openai"}},
	})
	if err := pm.SetActive("openai", "gpt-4o"); err != nil {
		t.Fatalf("SetActive: %v", err)
	}
	if err := pm.SetActive("nonexistent", ""); err == nil {
		t.Fatal("expected error for unknown provider")
	}

	reloaded := &config.Config{ActiveProvider: "kimi-code", Providers: []config.ProviderConfig{{ID: "kimi-code"}, {ID: "openai"}}}
	pm.SetConfig(reloaded)
	if got := pm.Config().ActiveProvider; got != "openai" {
		t.Errorf("ActiveProvider = %q, want openai (failed switch must not clear the earlier pick)", got)
	}
}
