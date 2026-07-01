// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
)

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
func TestProviderManagerSetActiveUnknown(t *testing.T) {
	cfg := &config.Config{Providers: []config.ProviderConfig{{ID: "openai"}}}
	pm := NewProviderManager(cfg)

	err := pm.SetActive("nonexistent", "")
	if err == nil {
		t.Error("Expected error for unknown provider")
	}
}

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
func TestProviderManagerListModelsUnknown(t *testing.T) {
	cfg := &config.Config{}
	pm := NewProviderManager(cfg)

	_, err := pm.ListModels("unknown")
	if err == nil {
		t.Error("Expected error for unknown provider")
	}
}

// TestResolveActiveModel_NoProvider verifies error when no active provider.
func TestResolveActiveModel_NoProvider(t *testing.T) {
	pm := NewProviderManager(&config.Config{})
	_, err := pm.ResolveActiveModel()
	if err == nil {
		t.Error("Expected error with no active provider")
	}
}

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

// TestBuildStreamOptions_NoProvider verifies BuildStreamOptions returns defaults with no provider.
func TestBuildStreamOptions_NoProvider(t *testing.T) {
	pm := NewProviderManager(&config.Config{})
	opts := pm.BuildStreamOptions()
	if opts.MaxRetries != 2 {
		t.Errorf("Default MaxRetries = %d, want 2", opts.MaxRetries)
	}
}

// TestBuildStreamOptions_WithProvider verifies BuildStreamOptions uses provider config.
func TestBuildStreamOptions_WithProvider(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "local",
		Providers: []config.ProviderConfig{
			{ID: "local", Endpoint: "http://localhost:9999/v1", APIKey: "test-key-123", MaxRetries: 5},
		},
	}
	pm := NewProviderManager(cfg)
	opts := pm.BuildStreamOptions()
	if opts.APIKey != "test-key-123" {
		t.Errorf("APIKey = %q, want %q", opts.APIKey, "test-key-123")
	}
	if opts.MaxRetries != 5 {
		t.Errorf("MaxRetries = %d, want 5", opts.MaxRetries)
	}
}

func TestInferProviderIdentity_Presets(t *testing.T) {
	tests := []struct {
		name     string
		id       string
		wantProv agenticprovider.Provider
		wantAPI  agenticprovider.Api
	}{
		{"openai", "openai", agenticprovider.ProviderOpenAI, agenticprovider.ApiOpenAICompletions},
		{"lmstudio", "lmstudio", agenticprovider.ProviderLMStudio, agenticprovider.ApiOpenAICompletions},
		{"ollama", "ollama", agenticprovider.ProviderOllama, agenticprovider.ApiOpenAICompletions},
		{"deepseek", "deepseek", agenticprovider.ProviderDeepSeek, agenticprovider.ApiOpenAICompletions},
		{"openrouter", "openrouter", agenticprovider.ProviderOpenRouter, agenticprovider.ApiOpenAICompletions},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prov, api := inferProviderIdentity(config.ProviderConfig{ID: tt.id})
			if prov != tt.wantProv {
				t.Errorf("Provider = %q, want %q", prov, tt.wantProv)
			}
			if api != tt.wantAPI {
				t.Errorf("API = %q, want %q", api, tt.wantAPI)
			}
		})
	}
}

func TestInferProviderIdentity_Localhost(t *testing.T) {
	tests := []struct {
		endpoint string
		wantProv agenticprovider.Provider
	}{
		{"http://localhost:1234/v1", agenticprovider.ProviderLMStudio},
		{"http://127.0.0.1:1234/v1", agenticprovider.ProviderLMStudio},
		{"http://localhost:11434/v1", agenticprovider.ProviderOllama},
		{"http://127.0.0.1:11434/v1", agenticprovider.ProviderOllama},
	}
	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			prov, _ := inferProviderIdentity(config.ProviderConfig{ID: "custom", Endpoint: tt.endpoint})
			if prov != tt.wantProv {
				t.Errorf("Provider = %q, want %q", prov, tt.wantProv)
			}
		})
	}
}

func TestInferProviderIdentity_ExplicitOverrides(t *testing.T) {
	prov, api := inferProviderIdentity(config.ProviderConfig{
		ID:       "custom",
		Provider: "anthropic",
		API:      "anthropic-messages",
	})
	if prov != agenticprovider.ProviderAnthropic {
		t.Errorf("Provider = %q, want %q", prov, agenticprovider.ProviderAnthropic)
	}
	if api != agenticprovider.ApiAnthropicMessages {
		t.Errorf("API = %q, want %q", api, agenticprovider.ApiAnthropicMessages)
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

func TestBuildStreamOptions_AllFields(t *testing.T) {
	temp := 0.7
	cfg := buildAllFieldsConfig(temp)
	pm := NewProviderManager(cfg)
	opts := pm.BuildStreamOptions()

	assertStreamProviderFields(t, opts)
	assertStreamModelFields(t, opts, temp)
}

func buildAllFieldsConfig(temp float64) *config.Config {
	cfg := &config.Config{
		ActiveProvider: "openai",
		Providers: []config.ProviderConfig{
			{
				ID:             "openai",
				Endpoint:       "https://api.openai.com/v1",
				APIKey:         "key",
				Timeout:        "30s",
				MaxRetries:     3,
				MaxRetryDelay:  "2s",
				Transport:      "sse",
				CacheRetention: "long",
				SessionID:      "session-1",
				Metadata:       map[string]string{"project": "goa"},
				Headers:        map[string]string{"X-Custom": "provider"},
			},
		},
		Models: []config.ModelConfig{
			{
				ID:          "gpt-4o",
				ProviderID:  "openai",
				Model:       "gpt-4o",
				Temperature: temp,
				MaxTokens:   1024,
				Headers:     map[string]string{"X-Custom": "model"},
			},
		},
	}
	cfg.ActiveModel = "gpt-4o"
	return cfg
}

func assertStreamProviderFields(t *testing.T, opts agenticprovider.StreamOptions) {
	t.Helper()
	if opts.APIKey != "key" {
		t.Errorf("APIKey = %q, want key", opts.APIKey)
	}
	if opts.Timeout != 30*time.Second {
		t.Errorf("Timeout = %v, want 30s", opts.Timeout)
	}
	if opts.MaxRetries != 3 {
		t.Errorf("MaxRetries = %d, want 3", opts.MaxRetries)
	}
	if opts.MaxRetryDelay != 2*time.Second {
		t.Errorf("MaxRetryDelay = %v, want 2s", opts.MaxRetryDelay)
	}
	if opts.Transport != agenticprovider.TransportSSE {
		t.Errorf("Transport = %q, want sse", opts.Transport)
	}
	if opts.CacheRetention != agenticprovider.CacheRetentionLong {
		t.Errorf("CacheRetention = %q, want long", opts.CacheRetention)
	}
	if opts.SessionID != "session-1" {
		t.Errorf("SessionID = %q, want session-1", opts.SessionID)
	}
	if opts.Metadata["project"] != "goa" {
		t.Errorf("Metadata project = %q, want goa", opts.Metadata["project"])
	}
}

func assertStreamModelFields(t *testing.T, opts agenticprovider.StreamOptions, wantTemp float64) {
	t.Helper()
	if opts.Temperature == nil || *opts.Temperature != wantTemp {
		t.Errorf("Temperature = %v, want %v", opts.Temperature, wantTemp)
	}
	if opts.MaxTokens != 1024 {
		t.Errorf("MaxTokens = %d, want 1024", opts.MaxTokens)
	}
	if opts.Headers["X-Custom"] != "model" {
		t.Errorf("Model header should override provider header, got %q", opts.Headers["X-Custom"])
	}
}

func TestBuildStreamOptions_DefaultsCacheRetentionToShort(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "openai",
		Providers: []config.ProviderConfig{
			{ID: "openai", Endpoint: "https://api.openai.com/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "gpt4o", ProviderID: "openai", Model: "gpt-4o"},
		},
	}
	cfg.ActiveModel = "gpt4o"
	pm := NewProviderManager(cfg)
	opts := pm.BuildStreamOptions()

	if opts.CacheRetention != agenticprovider.CacheRetentionShort {
		t.Errorf("CacheRetention = %q, want %q", opts.CacheRetention, agenticprovider.CacheRetentionShort)
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
				Reasoning:  true,
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
				Reasoning:  true,
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
				Reasoning:      true,
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
	if _, ok := mdl.ThinkingLevelMap[agenticprovider.ThinkingMedium]; !ok {
		t.Errorf("Expected ThinkingLevelMap to contain medium")
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
