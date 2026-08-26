// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
)

// TestProviderManagerActive verifies active provider selection.

// TestResolveActiveModel_NoProvider verifies error when no active provider.
func TestResolveActiveModel_NoProvider(t *testing.T) {
	pm := NewProviderManager(&config.Config{})
	_, err := pm.ResolveActiveModel()
	if err == nil {
		t.Error("Expected error with no active provider")
	}
}

// TestResolveActiveModel_NoModel verifies error when no model resolved.

// TestResolveActiveModel_NoModel verifies error when no model resolved.
func TestResolveActiveModel_NoModel(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "local", Endpoint: "http://localhost:9999/v1", DefaultModel: ""},
		},
	}
	pm := NewProviderManager(cfg)
	_, err := pm.ResolveActiveModel()
	if err == nil {
		t.Error("Expected error with no model name")
	}
}

// TestResolveActiveModel_Fallback verifies fallback to minimal model for custom providers.

// TestResolveActiveModel_Fallback verifies fallback to minimal model for custom providers.
func TestResolveActiveModel_Fallback(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "local",
		Providers: []config.ProviderConfig{
			{ID: "local", Endpoint: "http://localhost:9999/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "custom-model", ProviderID: "local", Model: "custom-model"},
		},
	}
	pm := NewProviderManager(cfg)
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel failed: %v", err)
	}
	if mdl.ID != "custom-model" {
		t.Errorf("Model.ID = %q, want %q", mdl.ID, "custom-model")
	}
	if mdl.BaseURL != "http://localhost:9999/v1/chat/completions" {
		t.Errorf("BaseURL = %q, want %q", mdl.BaseURL, "http://localhost:9999/v1/chat/completions")
	}
}

// TestResolveActiveModel_ZaiRawModelIDGetsThinking is the regression for
// "z.ai: no thinking shown": a glm model switched to as a raw ID (no
// ModelConfig, registry miss — e.g. a model the registry doesn't know) must
// still resolve with reasoning + zai thinking format so the thinking body is
// sent. Before the fix, the fallback path produced Reasoning=false and
// applyThinking skipped the body entirely.

// TestResolveActiveModel_ZaiRawModelIDGetsThinking is the regression for
// "z.ai: no thinking shown": a glm model switched to as a raw ID (no
// ModelConfig, registry miss — e.g. a model the registry doesn't know) must
// still resolve with reasoning + zai thinking format so the thinking body is
// sent. Before the fix, the fallback path produced Reasoning=false and
// applyThinking skipped the body entirely.
func TestResolveActiveModel_ZaiRawModelIDGetsThinking(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "zai",
		ActiveModel:    "glm-9.9-future", // unknown to the registry
		Providers: []config.ProviderConfig{
			{ID: "zai", Endpoint: "https://api.z.ai/api/coding/paas/v4"},
		},
	}
	pm := NewProviderManager(cfg)
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel failed: %v", err)
	}
	if !mdl.Reasoning {
		t.Error("Reasoning = false for z.ai fallback model, want true")
	}
	if mdl.ThinkingFormat != agenticprovider.ThinkingFormatZai {
		t.Errorf("ThinkingFormat = %q, want zai", mdl.ThinkingFormat)
	}
}

// TestBuildStreamOptions_NoProvider verifies BuildStreamOptions returns defaults with no provider.

// TestApplyModelConfigToFallback_ReasoningDefault verifies the tri-state
// reasoning semantics: omitted (nil) defaults to enabled, explicit false
// disables, explicit true enables.
func TestApplyModelConfigToFallback_ReasoningDefault(t *testing.T) {
	falseVal := false
	trueVal := true
	cases := []struct {
		name      string
		reasoning *bool
		want      bool
	}{
		{"omitted defaults to enabled", nil, true},
		{"explicit false disables", &falseVal, false},
		{"explicit true enables", &trueVal, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			mdl := agenticprovider.Model{ID: "m", Provider: agenticprovider.ProviderCustom, BaseURL: "https://x.example/v1"}
			applyModelConfigToFallback(&mdl, config.ModelConfig{ID: "m", Model: "m", Reasoning: tc.reasoning}, agenticprovider.ApiOpenAICompletions)
			if mdl.Reasoning != tc.want {
				t.Errorf("Reasoning = %v, want %v", mdl.Reasoning, tc.want)
			}
		})
	}
}

// TestApplyModelConfigCapabilities_ReasoningOverride verifies an explicit
// reasoning:false in model config overrides a registry model's built-in
// reasoning=true, while omitted leaves the registry value untouched.

// TestApplyModelConfigCapabilities_ReasoningOverride verifies an explicit
// reasoning:false in model config overrides a registry model's built-in
// reasoning=true, while omitted leaves the registry value untouched.
func TestApplyModelConfigCapabilities_ReasoningOverride(t *testing.T) {
	falseVal := false
	// Registry model starts with reasoning capability.
	mdl := agenticprovider.Model{ID: "m", Reasoning: true}
	applyModelConfigCapabilities(&mdl, config.ModelConfig{ID: "m", Model: "m", Reasoning: &falseVal}, agenticprovider.ApiOpenAICompletions)
	if mdl.Reasoning {
		t.Error("Reasoning = true, want false after explicit reasoning:false override")
	}

	mdl2 := agenticprovider.Model{ID: "m", Reasoning: false}
	applyModelConfigCapabilities(&mdl2, config.ModelConfig{ID: "m", Model: "m"}, agenticprovider.ApiOpenAICompletions)
	if mdl2.Reasoning {
		t.Error("Reasoning = true, want registry false preserved when reasoning omitted")
	}
}

func TestResolveActiveModel_ProviderIdentity(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "lmstudio",
		Providers: []config.ProviderConfig{
			{ID: "lmstudio", Endpoint: "http://localhost:1234/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "local-model", ProviderID: "lmstudio", Model: "local-model"},
		},
	}
	pm := NewProviderManager(cfg)
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel failed: %v", err)
	}
	if mdl.Provider != agenticprovider.ProviderLMStudio {
		t.Errorf("Provider = %q, want %q", mdl.Provider, agenticprovider.ProviderLMStudio)
	}
	if mdl.Api != agenticprovider.ApiOpenAICompletions {
		t.Errorf("API = %q, want %q", mdl.Api, agenticprovider.ApiOpenAICompletions)
	}
}

func TestResolveActiveModel_KnownModelViaLocalProvider(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "lmstudio",
		Providers: []config.ProviderConfig{
			{ID: "lmstudio", Endpoint: "http://localhost:1234/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "gemma-4-e4b", ProviderID: "lmstudio", Model: "gemma-4-e4b"},
		},
	}
	pm := NewProviderManager(cfg)
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel failed: %v", err)
	}
	if mdl.ID != "gemma-4-e4b" {
		t.Errorf("Model.ID = %q, want %q", mdl.ID, "gemma-4-e4b")
	}
	if mdl.ContextWindow <= 0 {
		t.Errorf("Model.ContextWindow = %d, want > 0", mdl.ContextWindow)
	}
	if mdl.Provider != agenticprovider.ProviderLMStudio {
		t.Errorf("Model.Provider = %q, want %q", mdl.Provider, agenticprovider.ProviderLMStudio)
	}
	if mdl.Api != agenticprovider.ApiOpenAICompletions {
		t.Errorf("Model.Api = %q, want %q", mdl.Api, agenticprovider.ApiOpenAICompletions)
	}
}

func TestResolveActiveModel_PrefixedKnownModel(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "lmstudio",
		Providers: []config.ProviderConfig{
			{ID: "lmstudio", Endpoint: "http://localhost:1234/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "google/gemma-4-e4b", ProviderID: "lmstudio", Model: "google/gemma-4-e4b"},
		},
	}
	pm := NewProviderManager(cfg)
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel failed: %v", err)
	}
	if mdl.ID != "google/gemma-4-e4b" {
		t.Errorf("Model.ID = %q, want %q", mdl.ID, "google/gemma-4-e4b")
	}
	if mdl.ContextWindow <= 0 {
		t.Errorf("Model.ContextWindow = %d, want > 0", mdl.ContextWindow)
	}
	if mdl.Provider != agenticprovider.ProviderLMStudio {
		t.Errorf("Model.Provider = %q, want %q", mdl.Provider, agenticprovider.ProviderLMStudio)
	}
	if mdl.Api != agenticprovider.ApiOpenAICompletions {
		t.Errorf("Model.Api = %q, want %q", mdl.Api, agenticprovider.ApiOpenAICompletions)
	}
}

// TestResolveModelByID verifies that a model config ID is resolved to the
// actual model name before building the agentic Model.
func TestResolveModelByID(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "openai",
		Providers: []config.ProviderConfig{
			{ID: "openai", Endpoint: "https://api.openai.com/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "gpt4o", ProviderID: "openai", Model: "gpt-4o"},
		},
	}
	pm := NewProviderManager(cfg)

	mdl, err := pm.ResolveModelByID("gpt4o")
	if err != nil {
		t.Fatalf("ResolveModelByID failed: %v", err)
	}
	if mdl.Name != "gpt-4o" {
		t.Errorf("Model.Name = %q, want %q", mdl.Name, "gpt-4o")
	}
	if mdl.BaseURL != "https://api.openai.com/v1/chat/completions" {
		t.Errorf("BaseURL = %q, want chat completions URL", mdl.BaseURL)
	}
}

// TestResolveModelForProvider verifies per-role provider/model resolution.

// TestResolveModelForProvider verifies per-role provider/model resolution.
func TestResolveModelForProvider(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "local",
		Providers: []config.ProviderConfig{
			{ID: "local", Endpoint: "http://localhost:1234/v1"},
			{ID: "remote", Endpoint: "http://remote.example.com/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "comp", ProviderID: "remote", Model: "companion-model"},
		},
	}
	pm := NewProviderManager(cfg)

	mdl, err := pm.ResolveModelForProvider("remote", "comp")
	if err != nil {
		t.Fatalf("ResolveModelForProvider failed: %v", err)
	}
	if mdl.Name != "companion-model" {
		t.Errorf("Model.Name = %q, want %q", mdl.Name, "companion-model")
	}
	if mdl.BaseURL != "http://remote.example.com/v1/chat/completions" {
		t.Errorf("BaseURL = %q, want remote chat completions URL", mdl.BaseURL)
	}

	_, err = pm.ResolveModelForProvider("unknown", "comp")
	if err != nil {
		t.Fatalf("ResolveModelForProvider should fall back to active provider: %v", err)
	}
}

func TestResolveActiveModel_ThinkingLevelMap(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "lmstudio",
		Providers: []config.ProviderConfig{
			{ID: "lmstudio", Endpoint: "http://localhost:1234/v1"},
		},
		Models: []config.ModelConfig{
			{
				ID:         "custom-model",
				ProviderID: "lmstudio",
				Model:      "custom-model",
				Reasoning:  agenticprovider.BoolPtr(true),
				ThinkingLevelMap: map[string]int{
					"low":    4096,
					"medium": 8192,
					"high":   16384,
				},
			},
		},
	}
	cfg.ActiveModel = "custom-model"
	pm := NewProviderManager(cfg)
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel failed: %v", err)
	}
	if mdl.ThinkingBudgets[agenticprovider.ThinkingLow] != 4096 {
		t.Errorf("low budget = %d, want 4096", mdl.ThinkingBudgets[agenticprovider.ThinkingLow])
	}
	if mdl.ThinkingBudgets[agenticprovider.ThinkingMedium] != 8192 {
		t.Errorf("medium budget = %d, want 8192", mdl.ThinkingBudgets[agenticprovider.ThinkingMedium])
	}
	if mdl.ThinkingBudgets[agenticprovider.ThinkingHigh] != 16384 {
		t.Errorf("high budget = %d, want 16384", mdl.ThinkingBudgets[agenticprovider.ThinkingHigh])
	}
}

func TestResolveActiveModel_DefaultThinkingLevelMap(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "lmstudio",
		Providers: []config.ProviderConfig{
			{ID: "lmstudio", Endpoint: "http://localhost:1234/v1"},
		},
		Models: []config.ModelConfig{
			{
				ID:         "custom-model",
				ProviderID: "lmstudio",
				Model:      "custom-model",
				Reasoning:  agenticprovider.BoolPtr(true),
			},
		},
	}
	cfg.ActiveModel = "custom-model"
	pm := NewProviderManager(cfg)
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel failed: %v", err)
	}
	want := config.DefaultThinkingLevelMap["medium"]
	if mdl.ThinkingBudgets[agenticprovider.ThinkingMedium] != want {
		t.Errorf("default medium budget = %d, want %d", mdl.ThinkingBudgets[agenticprovider.ThinkingMedium], want)
	}
}

func TestResolveActiveModel_ReasoningAndCompat(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "lmstudio",
		Providers: []config.ProviderConfig{
			{ID: "lmstudio", Endpoint: "http://localhost:1234/v1"},
		},
		Models: []config.ModelConfig{
			{
				ID:             "custom-model",
				ProviderID:     "lmstudio",
				Model:          "custom-model",
				Reasoning:      agenticprovider.BoolPtr(true),
				ThinkingLevel:  "medium",
				ThinkingBudget: 512,
				Compat:         `{"toolResultAsUser":true}`,
			},
		},
	}
	cfg.ActiveModel = "custom-model"
	pm := NewProviderManager(cfg)
	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel failed: %v", err)
	}
	if !mdl.Reasoning {
		t.Error("Expected Reasoning to be true")
	}
	if mdl.ThinkingBudgets[agenticprovider.ThinkingMedium] != 512 {
		t.Errorf("ThinkingBudget medium = %d, want 512", mdl.ThinkingBudgets[agenticprovider.ThinkingMedium])
	}
	compat, ok := mdl.Compat.(*agenticprovider.OpenAICompletionsCompat)
	if !ok {
		t.Fatalf("Compat type = %T, want *OpenAICompletionsCompat", mdl.Compat)
	}
	if compat.ToolResultAsUser == nil || !*compat.ToolResultAsUser {
		t.Errorf("Expected ToolResultAsUser=true")
	}
}

// TestProviderManagerSessionSelectionSurvivesHotReload reproduces the
// multi-instance provider bleed: instance A's /model switch persists
// active_provider/active_model to the shared config cascade; instance B's
// config watcher then hot-swaps a freshly loaded config via SetConfig.
// The session's explicit pick must survive that swap — the disk value is a
// startup default, not live session state (bug: B's footer AND next requests
// silently followed A's provider).
