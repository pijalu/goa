// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"os"
	"testing"
)

func TestGetEnvAPIKey_KnownProvider(t *testing.T) {
	os.Setenv("OPENAI_API_KEY", "sk-test-123")
	defer os.Unsetenv("OPENAI_API_KEY")

	key := GetEnvAPIKey(ProviderOpenAI)
	if key != "sk-test-123" {
		t.Errorf("expected 'sk-test-123', got %q", key)
	}
}

func TestGetEnvAPIKey_LocalProvider(t *testing.T) {
	key := GetEnvAPIKey(ProviderLMStudio)
	if key != "" {
		t.Errorf("expected empty for local provider, got %q", key)
	}
}

func TestGetEnvAPIKey_AnthropicPriority(t *testing.T) {
	os.Setenv("ANTHROPIC_OAUTH_TOKEN", "oat-abc")
	os.Setenv("ANTHROPIC_API_KEY", "sk-ant-xyz")
	defer os.Unsetenv("ANTHROPIC_OAUTH_TOKEN")
	defer os.Unsetenv("ANTHROPIC_API_KEY")

	key := GetEnvAPIKey(ProviderAnthropic)
	if key != "oat-abc" {
		t.Errorf("expected OATH token to take priority, got %q", key)
	}
}

func TestGetEnvAPIKey_UnknownProvider(t *testing.T) {
	os.Setenv("CUSTOM_API_KEY", "custom-key")
	defer os.Unsetenv("CUSTOM_API_KEY")

	key := GetEnvAPIKey(ProviderCustom)
	if key != "custom-key" {
		t.Errorf("expected 'custom-key', got %q", key)
	}
}

func TestGetEnvAPIKey_NoEnv(t *testing.T) {
	key := GetEnvAPIKey(ProviderDeepSeek)
	if key != "" {
		t.Errorf("expected empty, got %q", key)
	}
}

func TestToUpperSnakeCase(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"my-provider", "MY_PROVIDER"},
		{"openRouter", "OPEN_ROUTER"},
		{"OPENAI", "OPENAI"},
		{"lm-studio", "LM_STUDIO"},
		{"custom", "CUSTOM"},
		{"", ""},
		{"deepseek", "DEEPSEEK"},
		{"a", "A"},
		{"A", "A"},
		{"cloudflare-workers-ai", "CLOUDFLARE_WORKERS_AI"},
		{"openai", "OPENAI"},
		{"azureOpenAI", "AZURE_OPEN_AI"},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			got := toUpperSnakeCase(tt.input)
			if got != tt.want {
				t.Errorf("toUpperSnakeCase(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestBuildBaseOptions(t *testing.T) {
	model := Model{
		Headers: map[string]string{"User-Agent": "goa/1.0"},
	}

	opts := BuildBaseOptions(model, StreamOptions{})

	if opts.Transport != TransportSSE {
		t.Errorf("expected SSE transport, got %q", opts.Transport)
	}
	if opts.MaxRetries != 2 {
		t.Errorf("expected 2 max retries, got %d", opts.MaxRetries)
	}
	if opts.Headers["User-Agent"] != "goa/1.0" {
		t.Errorf("expected headers from model, got %v", opts.Headers)
	}
}

func TestBuildBaseOptions_PreservesExplicit(t *testing.T) {
	model := Model{}

	maxRetries := 5
	opts := BuildBaseOptions(model, StreamOptions{
		MaxRetries: maxRetries,
		Transport:  TransportWebSocket,
	})

	if opts.MaxRetries != 5 {
		t.Errorf("expected 5 max retries, got %d", opts.MaxRetries)
	}
	if opts.Transport != TransportWebSocket {
		t.Errorf("expected websocket transport, got %q", opts.Transport)
	}
}

func TestClampThinkingLevel_Off(t *testing.T) {
	model := Model{Reasoning: true}
	if got := ClampThinkingLevel(model, ThinkingOff); got != ThinkingOff {
		t.Errorf("expected off, got %q", got)
	}
	if got := ClampThinkingLevel(model, ""); got != ThinkingOff {
		t.Errorf("expected off for empty, got %q", got)
	}
}

func TestClampThinkingLevel_NoReasoning(t *testing.T) {
	model := Model{Reasoning: false}
	if got := ClampThinkingLevel(model, ThinkingHigh); got != ThinkingOff {
		t.Errorf("expected off for non-reasoning model, got %q", got)
	}
}

func TestClampThinkingLevel_WithMap(t *testing.T) {
	model := Model{
		Reasoning: true,
		ThinkingLevelMap: ThinkingLevelMap{
			ThinkingOff:  "none",
			ThinkingLow:  "low",
			ThinkingHigh: "high",
		},
	}

	// Level exists in map
	if got := ClampThinkingLevel(model, ThinkingLow); got != ThinkingLow {
		t.Errorf("expected low, got %q", got)
	}

	// Level not in map — clamp to nearest below
	if got := ClampThinkingLevel(model, ThinkingMedium); got != ThinkingLow {
		t.Errorf("expected clamped to low, got %q", got)
	}
}

func TestClampThinkingLevel_NoMap(t *testing.T) {
	model := Model{Reasoning: true}
	if got := ClampThinkingLevel(model, ThinkingHigh); got != ThinkingHigh {
		t.Errorf("expected high, got %q", got)
	}
	if got := ClampThinkingLevel(model, ThinkingXHigh); got != ThinkingMedium {
		t.Errorf("expected default medium for unknown level, got %q", got)
	}
}

func TestCalculateCost_FullPricing(t *testing.T) {
	model := Model{
		Cost: ModelPricing{
			Input:      0.000003, // $3/M input tokens
			Output:     0.000015, // $15/M output tokens
			CacheRead:  0.000001, // $1/M cache read tokens
			CacheWrite: 0.000002, // $2/M cache creation tokens
		},
	}

	usage := Usage{
		InputTokens:         1000,
		OutputTokens:        500,
		CacheReadTokens:     200,
		CacheCreationTokens: 50,
	}

	cost := CalculateCost(model, usage, "")
	expected := 1000*0.000003 + 500*0.000015 + 200*0.000001 + 50*0.000002
	if cost != expected {
		t.Errorf("expected %f, got %f", expected, cost)
	}
}

func TestCalculateCost_NoPricing(t *testing.T) {
	model := Model{}
	usage := Usage{InputTokens: 1000, OutputTokens: 500}
	cost := CalculateCost(model, usage, "")
	if cost != 0 {
		t.Errorf("expected 0 for no pricing, got %f", cost)
	}
}

func TestCalculateCost_PartialPricing(t *testing.T) {
	model := Model{
		Cost: ModelPricing{
			Input:  0.00001,
			Output: 0.00003,
		},
	}
	usage := Usage{InputTokens: 100, OutputTokens: 50}
	cost := CalculateCost(model, usage, "")
	expected := 100*0.00001 + 50*0.00003
	if cost != expected {
		t.Errorf("expected %f, got %f", expected, cost)
	}
}
