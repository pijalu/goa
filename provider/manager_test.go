// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/internal/auth"
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
func TestBuildStreamOptions_NoProvider(t *testing.T) {
	pm := NewProviderManager(&config.Config{})
	opts := pm.BuildStreamOptions()
	if opts.MaxRetries != 5 {
		t.Errorf("Default MaxRetries = %d, want 5", opts.MaxRetries)
	}
}

// TestBuildStreamOptions_UsesExecutionRetries verifies the global execution.retries
// setting drives the default when no per-provider max_retries is set.
func TestBuildStreamOptions_UsesExecutionRetries(t *testing.T) {
	pm := NewProviderManager(&config.Config{Execution: config.ExecutionConfig{Retries: 7}})
	opts := pm.BuildStreamOptions()
	if opts.MaxRetries != 7 {
		t.Errorf("Default MaxRetries from execution.retries = %d, want 7", opts.MaxRetries)
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

// TestBuildStreamOptions_RetryPolicyBeatsGlobal verifies the P8 (DS4)
// acceptance criterion: a per-provider retry_policy.max_retries beats the
// global execution.retries, and the resolved policy is carried in
// opts.RetryPolicy.
func TestBuildStreamOptions_RetryPolicyBeatsGlobal(t *testing.T) {
	cfg := &config.Config{
		Execution:      config.ExecutionConfig{Retries: 9},
		ActiveProvider: "openai",
		Providers: []config.ProviderConfig{
			{
				ID: "openai", Endpoint: "https://api.openai.com/v1", Provider: "openai",
				RetryPolicy: &config.RetryPolicyConfig{
					Mode:       "normal",
					MaxRetries: 1,
					Backoff:    config.RetryBackoffConfig{InitialMS: 500, MaxMS: 2000, Jitter: 0},
					Codes:      []string{"RATE_LIMIT"},
				},
			},
		},
	}
	pm := NewProviderManager(cfg)
	opts := pm.BuildStreamOptions()

	if opts.MaxRetries != 9 {
		t.Errorf("legacy MaxRetries = %d, want global 9 (unused when RetryPolicy set)", opts.MaxRetries)
	}
	if opts.RetryPolicy == nil {
		t.Fatal("expected resolved RetryPolicy on StreamOptions")
	}
	if opts.RetryPolicy.Mode != agenticprovider.RetryModeNormal {
		t.Errorf("RetryPolicy.Mode = %q, want normal", opts.RetryPolicy.Mode)
	}
	if opts.RetryPolicy.MaxRetries != 1 {
		t.Errorf("RetryPolicy.MaxRetries = %d, want 1 (per-provider beats global 9)", opts.RetryPolicy.MaxRetries)
	}
	if opts.RetryPolicy.Backoff.InitialDelay != 500*time.Millisecond {
		t.Errorf("RetryPolicy.Backoff.InitialDelay = %v, want 500ms", opts.RetryPolicy.Backoff.InitialDelay)
	}
	if opts.RetryPolicy.Backoff.MaxDelay != 2*time.Second {
		t.Errorf("RetryPolicy.Backoff.MaxDelay = %v, want 2s", opts.RetryPolicy.Backoff.MaxDelay)
	}
	if len(opts.RetryPolicy.Codes) != 1 || opts.RetryPolicy.Codes[0] != "RATE_LIMIT" {
		t.Errorf("RetryPolicy.Codes = %v, want [RATE_LIMIT]", opts.RetryPolicy.Codes)
	}
}

// TestBuildStreamOptions_RetryPolicyAlways verifies an always-mode
// retry_policy resolves through provider construction.
func TestBuildStreamOptions_RetryPolicyAlways(t *testing.T) {
	cfg := &config.Config{
		Execution:      config.ExecutionConfig{Retries: 9},
		ActiveProvider: "deepseek",
		Providers: []config.ProviderConfig{
			{
				ID: "deepseek", Endpoint: "https://api.deepseek.com", Provider: "deepseek",
				RetryPolicy: &config.RetryPolicyConfig{Mode: "always"},
			},
		},
	}
	pm := NewProviderManager(cfg)
	opts := pm.BuildStreamOptions()
	if opts.RetryPolicy == nil {
		t.Fatal("expected resolved RetryPolicy")
	}
	if opts.RetryPolicy.Mode != agenticprovider.RetryModeAlways {
		t.Errorf("RetryPolicy.Mode = %q, want always", opts.RetryPolicy.Mode)
	}
	// Always mode ignores the finite budget: MaxRetries defaults to the package
	// default even though execution.retries is 9.
	if opts.RetryPolicy.MaxRetries == 0 {
		t.Error("RetryPolicy.MaxRetries should be defaulted")
	}
}

// TestBuildStreamOptions_NoRetryPolicyKeepsLegacy verifies that omitting
// retry_policy leaves opts.RetryPolicy nil so the legacy scalar behavior
// (MaxRetries/MaxRetryDelay) applies unchanged.
func TestBuildStreamOptions_NoRetryPolicyKeepsLegacy(t *testing.T) {
	cfg := &config.Config{
		Execution:      config.ExecutionConfig{Retries: 3},
		ActiveProvider: "openai",
		Providers: []config.ProviderConfig{
			{ID: "openai", Endpoint: "https://api.openai.com/v1", Provider: "openai", MaxRetries: 4},
		},
	}
	pm := NewProviderManager(cfg)
	opts := pm.BuildStreamOptions()
	if opts.RetryPolicy != nil {
		t.Errorf("RetryPolicy = %+v, want nil (legacy scalar behavior)", opts.RetryPolicy)
	}
	if opts.MaxRetries != 4 {
		t.Errorf("MaxRetries = %d, want 4 (per-provider scalar)", opts.MaxRetries)
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
		{"zai coding preset", "zai", agenticprovider.ProviderZai, agenticprovider.ApiOpenAICompletions},
		{"zai api preset", "zai-api", agenticprovider.ProviderZaiApi, agenticprovider.ApiOpenAICompletions},
		{"poolside preset", "poolside", agenticprovider.ProviderPoolside, agenticprovider.ApiOpenAICompletions},
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

// TestInferProviderIdentity_ZaiEndpoints pins the endpoint heuristics for
// z.ai: the coding-plan endpoint maps to ProviderZai, the general API to
// ProviderZaiApi. The coding URL contains "api.z.ai" as a substring, so the
// ordering of endpointHeuristics matters — this guards against regressions
// that would route coding-plan users to the paid API identity.
func TestInferProviderIdentity_ZaiEndpoints(t *testing.T) {
	tests := []struct {
		endpoint string
		wantProv agenticprovider.Provider
	}{
		{"https://api.z.ai/api/coding/paas/v4", agenticprovider.ProviderZai},
		{"https://api.z.ai/api/paas/v4", agenticprovider.ProviderZaiApi},
		{"https://open.bigmodel.cn/api/coding/paas/v4", agenticprovider.ProviderZai},
		{"https://open.bigmodel.cn/api/paas/v4", agenticprovider.ProviderZaiApi},
	}
	for _, tt := range tests {
		t.Run(tt.endpoint, func(t *testing.T) {
			prov, api := inferProviderIdentity(config.ProviderConfig{ID: "custom", Endpoint: tt.endpoint})
			if prov != tt.wantProv {
				t.Errorf("Provider = %q, want %q", prov, tt.wantProv)
			}
			if api != agenticprovider.ApiOpenAICompletions {
				t.Errorf("API = %q, want openai-completions", api)
			}
		})
	}
}

// TestInferProviderIdentity_PoolsideEndpoint verifies the endpoint heuristic
// maps the poolside inference URL to the correct provider identity.
func TestInferProviderIdentity_PoolsideEndpoint(t *testing.T) {
	prov, api := inferProviderIdentity(config.ProviderConfig{ID: "custom", Endpoint: "https://inference.poolside.ai/v1"})
	if prov != agenticprovider.ProviderPoolside {
		t.Errorf("Provider = %q, want %q", prov, agenticprovider.ProviderPoolside)
	}
	if api != agenticprovider.ApiOpenAICompletions {
		t.Errorf("API = %q, want openai-completions", api)
	}
}

// TestStripKnownProviderPrefix_Zai verifies zai-prefixed model IDs resolve to
// their bare registry IDs (e.g. "zai/glm-5.2" → "glm-5.2").
func TestStripKnownProviderPrefix_Zai(t *testing.T) {
	tests := []struct{ in, want string }{
		{"zai/glm-5.2", "glm-5.2"},
		{"zai-api/glm-5.2", "glm-5.2"},
		{"glm-5.2", "glm-5.2"},
		{"unknown/glm-5.2", "unknown/glm-5.2"},
	}
	for _, tt := range tests {
		if got := stripKnownProviderPrefix(tt.in); got != tt.want {
			t.Errorf("stripKnownProviderPrefix(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

// TestInferProviderModelTraits_Poolside verifies poolside models get
// Reasoning=true so the thinking body is sent and thinking blocks display.
func TestInferProviderModelTraits_Poolside(t *testing.T) {
	mdl := agenticprovider.Model{
		ID:       "poolside-default",
		Provider: agenticprovider.ProviderPoolside,
		BaseURL:  "https://inference.poolside.ai/v1/chat/completions",
	}
	inferProviderModelTraits(&mdl)
	if !mdl.Reasoning {
		t.Error("Reasoning = false, want true for poolside")
	}
}

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

func TestDetectFromLMStudioModels_ContextLengthAlias(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "qwen/qwen3.5-9b", "max_context_length": 262144, "context_length": 32768},
			},
		})
	}))
	defer server.Close()

	baseURL, _ := url.Parse(server.URL)
	nCtx := detectFromLMStudioModels(&http.Client{Timeout: 5 * time.Second}, baseURL, "qwen/qwen3.5-9b", "")
	if nCtx != 32768 {
		t.Errorf("detectFromLMStudioModels = %d, want 32768 (context_length alias)", nCtx)
	}
}

func TestDetectFromLMStudioModels_LoadedContextLength(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "google/gemma-4-e4b", "max_context_length": 131072, "loaded_context_length": 8192},
				{"id": "other-model", "max_context_length": 4096},
			},
		})
	}))
	defer server.Close()

	baseURL, _ := url.Parse(server.URL)
	nCtx := detectFromLMStudioModels(&http.Client{Timeout: 5 * time.Second}, baseURL, "google/gemma-4-e4b", "")
	if nCtx != 8192 {
		t.Errorf("detectFromLMStudioModels = %d, want 8192", nCtx)
	}
}

func TestDetectFromLMStudioModels_FallsBackToMax(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "google/gemma-4-e4b", "max_context_length": 65536},
			},
		})
	}))
	defer server.Close()

	baseURL, _ := url.Parse(server.URL)
	nCtx := detectFromLMStudioModels(&http.Client{Timeout: 5 * time.Second}, baseURL, "google/gemma-4-e4b", "")
	if nCtx != 65536 {
		t.Errorf("detectFromLMStudioModels = %d, want 65536", nCtx)
	}
}

func TestDetectFromLMStudioModels_ModelNotFound(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "other-model", "max_context_length": 4096},
			},
		})
	}))
	defer server.Close()

	baseURL, _ := url.Parse(server.URL)
	nCtx := detectFromLMStudioModels(&http.Client{Timeout: 5 * time.Second}, baseURL, "missing-model", "")
	if nCtx != 0 {
		t.Errorf("detectFromLMStudioModels = %d, want 0", nCtx)
	}
}

func TestResolveActiveModel_NoEagerLocalContextDetection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v0/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "google/gemma-4-e4b", "max_context_length": 131072, "loaded_context_length": 8192},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	cfg := &config.Config{
		ActiveProvider: "lmstudio",
		Providers: []config.ProviderConfig{
			{ID: "lmstudio", Endpoint: server.URL + "/v1"},
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
	// ResolveActiveModel must not query the local provider before the model is
	// loaded, so it keeps the registry default rather than the loaded length.
	if mdl.ContextWindow != 131072 {
		t.Errorf("ContextWindow = %d, want 131072 (no eager detection before model is loaded)", mdl.ContextWindow)
	}
	// RefreshLocalContextWindow is the deferred path used after first tokens.
	if got := pm.RefreshLocalContextWindow(); got != 8192 {
		t.Errorf("RefreshLocalContextWindow = %d, want 8192", got)
	}
}

func TestDetectLocalContextWindow_LMStudio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/api/v0/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "google/gemma-4-e4b", "max_context_length": 131072, "loaded_context_length": 8192},
			},
		})
	}))
	defer server.Close()

	pCfg := config.ProviderConfig{ID: "lmstudio", Endpoint: server.URL + "/v1"}
	nCtx := detectLocalContextWindow(pCfg, "google/gemma-4-e4b", "")
	if nCtx != 8192 {
		t.Errorf("detectLocalContextWindow = %d, want 8192", nCtx)
	}
}

func TestDetectLocalContextWindow_NonLMStudio(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/v0/models" {
			_ = json.NewEncoder(w).Encode(map[string]any{
				"object": "list",
				"data": []map[string]any{
					{"id": "google/gemma-4-e4b", "max_context_length": 131072, "loaded_context_length": 8192},
				},
			})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	pCfg := config.ProviderConfig{ID: "ollama", Endpoint: server.URL + "/v1"}
	nCtx := detectLocalContextWindow(pCfg, "google/gemma-4-e4b", "")
	if nCtx != 0 {
		t.Errorf("detectLocalContextWindow for non-LM-Studio = %d, want 0", nCtx)
	}
}

func TestDetectFromModelMeta_LlamaCPP_LoadedContext(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "my-model", "meta": map[string]any{"n_ctx": 8192, "n_ctx_train": 131072}},
			},
		})
	}))
	defer server.Close()

	baseURL, _ := url.Parse(server.URL)
	nCtx := detectFromModelMeta(&http.Client{Timeout: 5 * time.Second}, baseURL, "my-model", "")
	if nCtx != 8192 {
		t.Errorf("detectFromModelMeta = %d, want 8192", nCtx)
	}
}

func TestDetectFromModelMeta_LlamaCPP_FallsBackToTrain(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/models" {
			w.WriteHeader(http.StatusNotFound)
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"object": "list",
			"data": []map[string]any{
				{"id": "my-model", "meta": map[string]any{"n_ctx_train": 131072}},
			},
		})
	}))
	defer server.Close()

	baseURL, _ := url.Parse(server.URL)
	nCtx := detectFromModelMeta(&http.Client{Timeout: 5 * time.Second}, baseURL, "my-model", "")
	if nCtx != 131072 {
		t.Errorf("detectFromModelMeta = %d, want 131072", nCtx)
	}
}

// mustAuthStore builds an auth store in a temp dir, failing the test on error.
func mustAuthStore(t *testing.T) *auth.Store {
	t.Helper()
	s, err := auth.NewStore(filepath.Join(t.TempDir(), "auth.json"))
	if err != nil {
		t.Fatalf("auth store: %v", err)
	}
	return s
}

func TestBuildStreamOptions_UsesAuthStoreAPIKey(t *testing.T) {
	store := mustAuthStore(t)
	_ = store.SetAPIKey("openai", "stored-key")

	cfg := &config.Config{
		ActiveProvider: "openai",
		Providers: []config.ProviderConfig{
			{ID: "openai", Endpoint: "https://api.openai.com/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "gpt-4o", ProviderID: "openai", Model: "gpt-4o"},
		},
	}
	cfg.ActiveModel = "gpt-4o"
	pm := NewProviderManager(cfg)
	pm.SetAuthStore(store)
	opts := pm.BuildStreamOptions()
	if opts.APIKey != "stored-key" {
		t.Errorf("APIKey = %q, want stored-key", opts.APIKey)
	}
}

func TestBuildStreamOptions_UsesAuthStoreOAuthAccessToken(t *testing.T) {
	store := mustAuthStore(t)
	_ = store.SetOAuth("openai", &oauth.Tokens{AccessToken: "oauth-access-token", TokenType: "bearer"})

	cfg := &config.Config{
		ActiveProvider: "openai",
		Providers: []config.ProviderConfig{
			{ID: "openai", Endpoint: "https://api.openai.com/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "gpt-4o", ProviderID: "openai", Model: "gpt-4o"},
		},
	}
	cfg.ActiveModel = "gpt-4o"
	pm := NewProviderManager(cfg)
	pm.SetAuthStore(store)
	opts := pm.BuildStreamOptions()
	if opts.APIKey != "oauth-access-token" {
		t.Errorf("APIKey = %q, want oauth-access-token", opts.APIKey)
	}
}

// TestListModels_UsesAuthStoreAPIKey verifies ListModels resolves the API key
// from the auth store when the provider config has none (E1): OAuth/API-key

// TestActive_NilReceiver verifies Active is safe on a nil *ProviderManager
// (typed-nil interface), returning no provider instead of panicking.
func TestActive_NilReceiver(t *testing.T) {
	var pm *ProviderManager
	p, model := pm.Active()
	if p != nil || model != "" {
		t.Errorf("Active() = (%v, %q), want (nil, \"\")", p, model)
	}
}

// TestResolveAPIKey_AuthStoreFallback verifies ResolveAPIKey returns the key
// from the auth store when ProviderConfig.APIKey is empty (the /login case),
// so plugins see the provider as authenticated (z.ai #6).
func TestResolveAPIKey_AuthStoreFallback(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "zai", Provider: "zai"}},
	}
	pm := NewProviderManager(cfg)
	store, err := auth.NewStore("")
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.SetAPIKey("zai", "test-zai-key"); err != nil {
		t.Fatalf("SetAPIKey: %v", err)
	}
	pm.SetAuthStore(store)

	if got := pm.ResolveAPIKey("zai"); got != "test-zai-key" {
		t.Errorf("ResolveAPIKey = %q, want %q (auth store fallback)", got, "test-zai-key")
	}
	if got := pm.ResolveAPIKey("unknown"); got != "" {
		t.Errorf("ResolveAPIKey(unknown) = %q, want empty", got)
	}
	var nilPM *ProviderManager
	if got := nilPM.ResolveAPIKey("zai"); got != "" {
		t.Errorf("nil ResolveAPIKey = %q, want empty", got)
	}
}

// TestResolveAPIKey_ConfigKeyWins verifies an explicit ProviderConfig.APIKey
// takes precedence over the auth store.
func TestResolveAPIKey_ConfigKeyWins(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{{ID: "zai", Provider: "zai", APIKey: "config-key"}},
	}
	pm := NewProviderManager(cfg)
	store, _ := auth.NewStore("")
	_ = store.SetAPIKey("zai", "store-key")
	pm.SetAuthStore(store)
	if got := pm.ResolveAPIKey("zai"); got != "config-key" {
		t.Errorf("ResolveAPIKey = %q, want config key %q", got, "config-key")
	}
}

// TestSetConfig_HotReloadSwapsActiveProvider verifies SetConfig swaps the
// config the next request resolves: after a hot reload, Active() and
// BuildStreamOptions() reflect the new provider profile (P22/DS6).
func TestSetConfig_HotReloadSwapsActiveProvider(t *testing.T) {
	oldCfg := &config.Config{
		ActiveProvider: "old-provider",
		ActiveModel:    "old-model",
		Providers: []config.ProviderConfig{{
			ID:       "old-provider",
			Endpoint: "https://old.example/v1",
		}},
		Models: []config.ModelConfig{{ID: "old-model", ProviderID: "old-provider", Model: "old-model"}},
	}
	pm := NewProviderManager(oldCfg)

	oldProvider, oldModel := pm.Active()
	if oldProvider == nil || oldProvider.ID != "old-provider" || oldModel != "old-model" {
		t.Fatalf("boot Active() = (%v, %q), want (old-provider, old-model)", oldProvider, oldModel)
	}

	newCfg := &config.Config{
		ActiveProvider: "new-provider",
		ActiveModel:    "new-model",
		Providers: []config.ProviderConfig{{
			ID:       "new-provider",
			Endpoint: "https://new.example/v1",
		}},
		Models: []config.ModelConfig{{ID: "new-model", ProviderID: "new-provider", Model: "new-model"}},
	}
	pm.SetConfig(newCfg)

	// Next request sees the new profile.
	newProvider, newModel := pm.Active()
	if newProvider == nil || newProvider.ID != "new-provider" || newModel != "new-model" {
		t.Errorf("after SetConfig Active() = (%v, %q), want (new-provider, new-model)", newProvider, newModel)
	}
	if pm.Config() != newCfg {
		t.Errorf("Config() did not return the reloaded config")
	}

	mdl, err := pm.ResolveActiveModel()
	if err != nil {
		t.Fatalf("ResolveActiveModel: %v", err)
	}
	if mdl.BaseURL != "https://new.example/v1/chat/completions" {
		t.Errorf("ResolveActiveModel BaseURL = %q, want new-provider endpoint", mdl.BaseURL)
	}
}

// TestSetConfig_NilConfigSafe verifies SetConfig(nil) and Config() are safe.
func TestSetConfig_NilConfigSafe(t *testing.T) {
	pm := NewProviderManager(&config.Config{})
	pm.SetConfig(nil)
	if pm.Config() != nil {
		t.Errorf("Config() after SetConfig(nil) = %v, want nil", pm.Config())
	}
	if p, m := pm.Active(); p != nil || m != "" {
		t.Errorf("Active() after SetConfig(nil) = (%v, %q), want (nil, \"\")", p, m)
	}
}

// TestListRegistryModels_OpenAICodexServesCodexFamily pins the codex registry
// alias: the openai-codex subscription provider has no models.dev mapping of
// its own and its endpoint serves no /models route, so ListRegistryModels
// must alias to the openai catalog filtered to the codex family (the picker
// showed ONLY "custom model" before the alias, behind a misleading "using
// known models" flash).
func TestListRegistryModels_OpenAICodexServesCodexFamily(t *testing.T) {
	cfg := &config.Config{
		Providers: []config.ProviderConfig{
			{ID: "codex", Name: "OpenAI Codex", Endpoint: "https://chatgpt.com/backend-api"},
			{ID: "openai", Name: "OpenAI", Endpoint: "https://api.openai.com/v1"},
		},
	}
	pm := NewProviderManager(cfg)

	codex := pm.ListRegistryModels("codex")
	if len(codex) == 0 {
		t.Fatal("ListRegistryModels(openai-codex endpoint) returned no models; the picker can only offer a custom row")
	}
	hasSpark := false
	for _, m := range codex {
		if m.ID == "gpt-5.3-codex-spark" {
			hasSpark = true
		}
		if !isCodexFamilyModel(m.ID) {
			t.Errorf("non-codex-family model %q leaked into the openai-codex list", m.ID)
		}
	}
	if !hasSpark {
		t.Errorf("codex list missing gpt-5.3-codex-spark (Pi codex catalog); got %v", codex)
	}

	// The plain openai provider list must be unaffected by the alias.
	openai := pm.ListRegistryModels("openai")
	if len(openai) == 0 {
		t.Fatal("ListRegistryModels(openai) returned no models")
	}
	hasGPT4o := false
	for _, m := range openai {
		if m.ID == "gpt-4o" {
			hasGPT4o = true
		}
	}
	if !hasGPT4o {
		t.Error("openai registry list lost gpt-4o — the codex alias must not narrow the openai list")
	}
}

// TestIsCodexFamilyModel pins the codex-served model filter: the codex
// subscription endpoint serves the explicit codex models plus the gpt-5.4+
// generations (Pi scripts/generate-models.ts codexModels).
func TestIsCodexFamilyModel(t *testing.T) {
	tests := []struct {
		id   string
		want bool
	}{
		{"gpt-5.3-codex-spark", true},
		{"gpt-5.3-codex", true},
		{"gpt-5.4", true},
		{"gpt-5.4-mini", true},
		{"gpt-5.5", true},
		{"gpt-5.6-luna", true},
		{"gpt-5.6-sol", true},
		{"gpt-5.6-terra", true},
		{"gpt-5", false},
		{"gpt-5.1", false},
		{"gpt-5.2", false},
		{"gpt-5.2-chat-latest", false},
		{"gpt-5.4-pro", false},
		{"gpt-5.4-nano", false},
		{"gpt-4o", false},
		{"gpt-4.1", false},
		{"o3", false},
		{"chatgpt-image-latest", false},
	}
	for _, tt := range tests {
		if got := isCodexFamilyModel(tt.id); got != tt.want {
			t.Errorf("isCodexFamilyModel(%q) = %v, want %v", tt.id, got, tt.want)
		}
	}
}
