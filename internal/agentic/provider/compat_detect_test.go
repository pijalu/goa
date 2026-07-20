// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"
)

func TestDetectOpenAICompat_StandardOpenAI(t *testing.T) {
	compat := DetectOpenAICompat(Model{Provider: ProviderOpenAI, BaseURL: "https://api.openai.com/v1"})
	if !*compat.SupportsStore {
		t.Error("expected SupportsStore=true for OpenAI")
	}
	if !*compat.SupportsDeveloperRole {
		t.Error("expected SupportsDeveloperRole=true for OpenAI")
	}
	if !*compat.SupportsReasoningEffort {
		t.Error("expected SupportsReasoningEffort=true for OpenAI")
	}
	if !*compat.SupportsUsageInStreaming {
		t.Error("expected SupportsUsageInStreaming=true")
	}
	if *compat.MaxTokensField != "max_completion_tokens" {
		t.Errorf("expected max_completion_tokens, got %q", *compat.MaxTokensField)
	}
	if !*compat.SupportsStrictMode {
		t.Error("expected SupportsStrictMode=true for OpenAI")
	}
	if !*compat.SupportsLongCacheRetention {
		t.Error("expected SupportsLongCacheRetention=true for OpenAI")
	}
}

func TestDetectOpenAICompat_DeepSeek(t *testing.T) {
	compat := DetectOpenAICompat(Model{Provider: ProviderDeepSeek, BaseURL: "https://api.deepseek.com"})
	if *compat.ThinkingFormat != "deepseek" {
		t.Errorf("expected deepseek thinking format, got %q", *compat.ThinkingFormat)
	}
	if !*compat.RequiresReasoningContentOnAssistantMessages {
		t.Error("expected RequiresReasoningContentOnAssistantMessages=true for DeepSeek")
	}
	if *compat.SupportsStore {
		t.Error("expected SupportsStore=false for DeepSeek (non-standard)")
	}
}

func TestDetectOpenAICompat_Together(t *testing.T) {
	compat := DetectOpenAICompat(Model{Provider: ProviderTogether, BaseURL: "https://api.together.ai"})
	if *compat.MaxTokensField != "max_tokens" {
		t.Errorf("expected max_tokens, got %q", *compat.MaxTokensField)
	}
	if *compat.SupportsReasoningEffort {
		t.Error("expected SupportsReasoningEffort=false for Together")
	}
	if *compat.ThinkingFormat != "together" {
		t.Errorf("expected together thinking format, got %q", *compat.ThinkingFormat)
	}
	if *compat.SupportsLongCacheRetention {
		t.Error("expected SupportsLongCacheRetention=false for Together")
	}
}

func TestDetectOpenAICompat_OpenRouter(t *testing.T) {
	compat := DetectOpenAICompat(Model{Provider: ProviderOpenRouter, BaseURL: "https://openrouter.ai/api/v1"})
	if *compat.ThinkingFormat != "openrouter" {
		t.Errorf("expected openrouter thinking format, got %q", *compat.ThinkingFormat)
	}
	if *compat.CacheControlFormat != "anthropic" {
		t.Errorf("expected anthropic cache control format for OpenRouter, got %q", *compat.CacheControlFormat)
	}
}

func TestDetectOpenAICompat_XAI(t *testing.T) {
	compat := DetectOpenAICompat(Model{Provider: "xai", BaseURL: "https://api.x.ai"})
	if *compat.SupportsReasoningEffort {
		t.Error("expected SupportsReasoningEffort=false for xAI/Grok")
	}
	if *compat.SupportsStore {
		t.Error("expected SupportsStore=false for xAI (non-standard)")
	}
}

func TestDetectOpenAICompat_ZAI(t *testing.T) {
	compat := DetectOpenAICompat(Model{Provider: "zai", BaseURL: "https://api.z.ai/v1"})
	if *compat.ThinkingFormat != "zai" {
		t.Errorf("expected zai thinking format, got %q", *compat.ThinkingFormat)
	}
	if *compat.SupportsReasoningEffort {
		t.Error("expected SupportsReasoningEffort=false for z.ai")
	}
}

func TestDetectOpenAICompat_ZaiVariants(t *testing.T) {
	cases := []struct {
		name     string
		provider Provider
		baseURL  string
	}{
		{"coding plan by provider name", ProviderZai, "https://api.z.ai/api/coding/paas/v4"},
		{"general api by provider name", ProviderZaiApi, "https://api.z.ai/api/paas/v4"},
		{"coding plan by URL", "custom", "https://api.z.ai/api/coding/paas/v4"},
		{"general api by URL", "custom", "https://api.z.ai/api/paas/v4"},
		{"CN bigmodel coding by URL", "custom", "https://open.bigmodel.cn/api/coding/paas/v4"},
		{"CN bigmodel by URL", "custom", "https://open.bigmodel.cn/api/paas/v4"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			compat := DetectOpenAICompat(Model{Provider: tc.provider, BaseURL: tc.baseURL})
			if *compat.ThinkingFormat != "zai" {
				t.Errorf("ThinkingFormat = %q, want zai", *compat.ThinkingFormat)
			}
			if *compat.SupportsReasoningEffort {
				t.Error("SupportsReasoningEffort = true, want false for z.ai")
			}
			if *compat.SupportsStore {
				t.Error("SupportsStore = true, want false for z.ai (non-standard)")
			}
			if *compat.SupportsDeveloperRole {
				t.Error("SupportsDeveloperRole = true, want false for z.ai (non-standard)")
			}
		})
	}
}

func TestDetectOpenAICompat_CustomDefaults(t *testing.T) {
	// Unknown provider should get OpenAI defaults (no detection overrides)
	compat := DetectOpenAICompat(Model{Provider: ProviderCustom, BaseURL: "https://custom.example.com"})
	if !*compat.SupportsStore {
		t.Error("expected SupportsStore=true for custom (OpenAI defaults)")
	}
	if !*compat.SupportsReasoningEffort {
		t.Error("expected SupportsReasoningEffort=true for custom")
	}
	if *compat.MaxTokensField != "max_completion_tokens" {
		t.Errorf("expected max_completion_tokens, got %q", *compat.MaxTokensField)
	}
	if *compat.ThinkingFormat != "openai" {
		t.Errorf("expected openai thinking format, got %q", *compat.ThinkingFormat)
	}
}

func TestResolveOpenAICompat_ExplicitMerge(t *testing.T) {
	model := Model{
		Provider: ProviderTogether,
		BaseURL:  "https://api.together.ai",
		Compat: OpenAICompletionsCompat{
			SupportsReasoningEffort: boolPtr(true),
		},
	}

	compat := ResolveOpenAICompat(model)
	if !*compat.SupportsReasoningEffort {
		t.Error("expected explicit SupportsReasoningEffort=true to override detection")
	}
	// Other fields should still be detected
	if *compat.MaxTokensField != "max_tokens" {
		t.Errorf("expected max_tokens from detection, got %q", *compat.MaxTokensField)
	}
}

func TestResolveOpenAICompat_NoCompat(t *testing.T) {
	model := Model{
		Provider: ProviderDeepSeek,
		BaseURL:  "https://api.deepseek.com",
	}
	// Compat is nil — should use detected values
	compat := ResolveOpenAICompat(model)
	if *compat.ThinkingFormat != "deepseek" {
		t.Errorf("expected deepseek thinking format, got %q", *compat.ThinkingFormat)
	}
}

func TestDetectAnthropicCompat_Standard(t *testing.T) {
	compat := DetectAnthropicCompat(ProviderAnthropic, "https://api.anthropic.com")
	if !*compat.SupportsEagerToolInputStreaming {
		t.Error("expected SupportsEagerToolInputStreaming=true for Anthropic")
	}
	if !*compat.SupportsLongCacheRetention {
		t.Error("expected SupportsLongCacheRetention=true for Anthropic")
	}
	if *compat.SendSessionAffinityHeaders {
		t.Error("expected SendSessionAffinityHeaders=false for Anthropic")
	}
}

func TestDetectAnthropicCompat_Fireworks(t *testing.T) {
	compat := DetectAnthropicCompat(ProviderFireworks, "https://api.fireworks.ai")
	if *compat.SupportsEagerToolInputStreaming {
		t.Error("expected SupportsEagerToolInputStreaming=false for Fireworks")
	}
	if *compat.SupportsLongCacheRetention {
		t.Error("expected SupportsLongCacheRetention=false for Fireworks")
	}
	if !*compat.SendSessionAffinityHeaders {
		t.Error("expected SendSessionAffinityHeaders=true for Fireworks")
	}
}

func TestResolveCompat_DispatchOpenAI(t *testing.T) {
	model := Model{
		Api:      ApiOpenAICompletions,
		Provider: ProviderDeepSeek,
		BaseURL:  "https://api.deepseek.com",
	}
	result := ResolveCompat(model)
	compat, ok := result.(OpenAICompletionsCompat)
	if !ok {
		t.Fatal("expected OpenAICompletionsCompat")
	}
	if *compat.ThinkingFormat != "deepseek" {
		t.Errorf("expected deepseek thinking format, got %q", *compat.ThinkingFormat)
	}
}

func TestResolveCompat_DispatchAnthropic(t *testing.T) {
	model := Model{
		Api:      ApiAnthropicMessages,
		Provider: ProviderAnthropic,
		BaseURL:  "https://api.anthropic.com",
	}
	result := ResolveCompat(model)
	_, ok := result.(AnthropicMessagesCompat)
	if !ok {
		t.Fatal("expected AnthropicMessagesCompat")
	}
}

func TestResolveCompat_UnknownAPI(t *testing.T) {
	model := Model{
		Api:      "unknown-api",
		Provider: ProviderCustom,
	}
	result := ResolveCompat(model)
	if result != nil {
		t.Error("expected nil for unknown API type")
	}
}

func TestToBool(t *testing.T) {
	if ToBool(nil, true) != true {
		t.Error("expected fallback true")
	}
	if ToBool(nil, false) != false {
		t.Error("expected fallback false")
	}
	v := true
	if ToBool(&v, false) != true {
		t.Error("expected value true")
	}
	v = false
	if ToBool(&v, true) != false {
		t.Error("expected value false")
	}
}

func TestToString(t *testing.T) {
	if ToString(nil, "default") != "default" {
		t.Error("expected fallback")
	}
	v := "explicit"
	if ToString(&v, "default") != "explicit" {
		t.Error("expected explicit value")
	}
}

func TestDetectOpenAICompat_SupportsCacheRetention(t *testing.T) {
	tests := []struct {
		name   Provider
		url    string
		expect bool
	}{
		{"openai", "https://api.openai.com", true},
		{"together", "https://api.together.ai", false},
		{"nvidia", "https://integrate.api.nvidia.com", false},
		{"ant-ling", "https://api.ant-ling.com", false},
		{"custom", "https://custom.api.com", true},
	}
	for _, tt := range tests {
		t.Run(string(tt.name), func(t *testing.T) {
			compat := DetectOpenAICompat(Model{Provider: tt.name, BaseURL: tt.url})
			if *compat.SupportsLongCacheRetention != tt.expect {
				t.Errorf("expected SupportsLongCacheRetention=%v for %s, got %v", tt.expect, tt.name, *compat.SupportsLongCacheRetention)
			}
		})
	}
}
