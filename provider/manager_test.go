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

// TestBuildStreamOptions_CacheRetentionPrecedence pins the cache-affinity
// default (bugs.md 2026-08-19): with no user cache_retention the z.ai catalog
// default (long) applies so the session cache identity reaches the wire as
// prompt_cache_key; an explicit user setting still wins; providers without a
// catalog default stay short. The zai entry mirrors the real user-config
// shape (id + endpoint, no provider field) to exercise the URL/ID fallbacks.
func TestBuildStreamOptions_CacheRetentionPrecedence(t *testing.T) {
	zaiCfg := func(retention string) *config.Config {
		return &config.Config{
			ActiveProvider: "zai",
			Providers: []config.ProviderConfig{{
				ID:             "zai",
				Endpoint:       "https://api.z.ai/api/coding/paas/v4",
				APIKey:         "k",
				CacheRetention: retention,
			}},
		}
	}

	t.Run("catalog default applies when user is silent", func(t *testing.T) {
		opts := NewProviderManager(zaiCfg("")).BuildStreamOptions()
		if opts.CacheRetention != agenticprovider.CacheRetentionLong {
			t.Errorf("CacheRetention = %q, want long (zai catalog default)", opts.CacheRetention)
		}
	})
	t.Run("explicit user setting beats catalog default", func(t *testing.T) {
		opts := NewProviderManager(zaiCfg("short")).BuildStreamOptions()
		if opts.CacheRetention != agenticprovider.CacheRetentionShort {
			t.Errorf("CacheRetention = %q, want short (user override)", opts.CacheRetention)
		}
	})
	t.Run("provider without catalog default stays short", func(t *testing.T) {
		cfg := &config.Config{
			ActiveProvider: "local",
			Providers: []config.ProviderConfig{{
				ID: "local", Endpoint: "http://localhost:9999/v1", APIKey: "k",
			}},
		}
		opts := NewProviderManager(cfg).BuildStreamOptions()
		if opts.CacheRetention != agenticprovider.CacheRetentionShort {
			t.Errorf("CacheRetention = %q, want short (global default)", opts.CacheRetention)
		}
	})
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
		{"openai", "openai", agenticprovider.ProviderOpenAI, agenticprovider.ApiOpenAIResponses},
		{"openai-codex", "openai-codex", agenticprovider.ProviderOpenAICodex, agenticprovider.ApiOpenAICodexResponses},
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
