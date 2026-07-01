// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"encoding/json"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func ptr[T any](v T) *T { return &v }

func TestOpenAICompletionsProtocolBuildsRequest(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	ctx := schema.Context{
		Messages: []schema.Message{schema.NewUserMessage("hello")},
	}
	maxTokens := 1024
	opts := schema.StreamOptions{MaxTokens: maxTokens}
	profile := schema.VariantProfile{
		Compat: schema.CompatFlags{MaxTokensField: "max_tokens"},
	}

	body, err := p.BuildRequest(schema.Model{ID: "gpt-4o", Api: schema.ApiOpenAICompletions}, ctx, opts, profile)
	require.NoError(t, err)

	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	assert.Equal(t, "gpt-4o", req["model"])
	assert.Equal(t, float64(1024), req["max_tokens"])
}

func TestRegisteredAPIs(t *testing.T) {
	apis := RegisteredAPIs()
	require.NotEmpty(t, apis)
	assert.Contains(t, apis, schema.ApiOpenAICompletions)
	assert.Contains(t, apis, schema.ApiAnthropicMessages)
}

func TestGeminiThinkingBudget(t *testing.T) {
	p := ForAPI(schema.ApiGoogleGenerativeAI)
	require.NotNil(t, p)

	profile := schema.VariantProfile{
		Defaults: schema.Defaults{
			ThinkingBudgets: schema.ThinkingBudgets{schema.ThinkingMedium: 1024},
		},
	}
	body, err := p.BuildRequest(
		schema.Model{ID: "gemini-3.1-pro", Api: schema.ApiGoogleGenerativeAI, Reasoning: true},
		schema.Context{},
		schema.StreamOptions{},
		profile,
	)
	require.NoError(t, err)

	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	cfg, ok := req["thinkingConfig"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, float64(1024), cfg["thinkingBudget"])
}

func TestAnthropicOAuthHeaders(t *testing.T) {
	p := ForAPI(schema.ApiAnthropicMessages)
	require.NotNil(t, p)

	profile := schema.VariantProfile{
		Auth: schema.AuthConfig{
			Method: schema.AuthMethodOAuth,
			OAuthIdentity: []schema.HeaderRule{
				{Name: "X-Claude-Client-Identity", Value: "goa"},
			},
		},
	}
	headers := p.RequestHeaders(schema.Model{}, profile)
	assert.Equal(t, "goa", headers["X-Claude-Client-Identity"])
	assert.Contains(t, headers["anthropic-beta"], "claude-code-20250219")
}

func TestAnthropicCacheBreakpointCap(t *testing.T) {
	p := ForAPI(schema.ApiAnthropicMessages)
	require.NotNil(t, p)

	ctx := schema.Context{
		Messages: []schema.Message{
			schema.NewUserMessage("first"),
			schema.NewUserMessage("second"),
			schema.NewUserMessage("third"),
		},
	}
	profile := schema.VariantProfile{
		CachePolicy: schema.CachePolicy{BreakpointCap: 4, TTL: "1h"},
	}
	body, err := p.BuildRequest(schema.Model{Api: schema.ApiAnthropicMessages}, ctx, schema.StreamOptions{}, profile)
	require.NoError(t, err)

	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	msgs, ok := req["messages"].([]any)
	require.True(t, ok)
	require.Len(t, msgs, 3)
}

func TestAnthropicAdaptiveThinking(t *testing.T) {
	p := ForAPI(schema.ApiAnthropicMessages)
	require.NotNil(t, p)

	profile := schema.VariantProfile{}
	body, err := p.BuildRequest(
		schema.Model{ID: "claude-opus-4", Api: schema.ApiAnthropicMessages, Reasoning: true},
		schema.Context{},
		schema.StreamOptions{},
		profile,
	)
	require.NoError(t, err)

	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	thinking, ok := req["thinking"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "adaptive", thinking["type"])
}

func TestOpenAIResponsesBuildsRequest(t *testing.T) {
	p := ForAPI(schema.ApiOpenAIResponses)
	require.NotNil(t, p)

	ctx := schema.Context{
		Messages: []schema.Message{
			schema.NewSystemMessage("sys"),
			schema.NewUserMessage("hello"),
		},
	}
	body, err := p.BuildRequest(
		schema.Model{ID: "gpt-5", Api: schema.ApiOpenAIResponses, Reasoning: true},
		ctx,
		schema.StreamOptions{ServiceTier: "high"},
		schema.VariantProfile{Compat: schema.CompatFlags{SupportsStore: ptr(false)}},
	)
	require.NoError(t, err)

	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	assert.Equal(t, "gpt-5", req["model"])
	assert.Equal(t, "high", req["service_tier"])
	assert.Equal(t, false, req["store"])
	assert.Contains(t, req["include"], "reasoning.encrypted_content")

	input, ok := req["input"].([]any)
	require.True(t, ok)
	require.Len(t, input, 2)
	first := input[0].(map[string]any)
	assert.Equal(t, "developer", first["role"])
}

func TestLMStudioQwenRequest(t *testing.T) {
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	profile := schema.VariantProfile{
		Compat: schema.CompatFlags{
			MaxTokensField: "max_tokens",
			ThinkingFormat: "qwen",
		},
		Auth: schema.AuthConfig{Method: schema.AuthMethodNone, Required: false},
	}
	model := schema.Model{
		ID:       "qwen/qwen3.5-9b",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderLMStudio,
		BaseURL:  "http://localhost:1234/v1/chat/completions",
	}
	ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("hello")}}
	body, err := p.BuildRequest(model, ctx, schema.StreamOptions{MaxTokens: 512}, profile)
	require.NoError(t, err)

	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	assert.Equal(t, "qwen/qwen3.5-9b", req["model"])
	assert.Equal(t, true, req["thinking"])
	assert.Equal(t, float64(512), req["max_tokens"])
}

func TestOpenAICompletionsThinkingFormats(t *testing.T) {
	tests := []struct {
		format    string
		reasoning string
		want      map[string]any
	}{
		{"openai", "high", map[string]any{"reasoning_effort": "high"}},
		{"deepseek", "medium", map[string]any{"thinking": map[string]any{"type": "enabled"}, "reasoning_effort": "medium"}},
		{"zai", "low", map[string]any{"thinking": map[string]any{"type": "enabled", "clear_thinking": false}}},
		{"together", "high", map[string]any{"reasoning": map[string]any{"enabled": true}, "reasoning_effort": "high"}},
		{"openrouter", "medium", map[string]any{"reasoning": map[string]any{"effort": "medium"}}},
	}

	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)

	for _, tt := range tests {
		t.Run(tt.format, func(t *testing.T) {
			profile := schema.VariantProfile{
				Compat:   schema.CompatFlags{ThinkingFormat: tt.format},
				Defaults: schema.Defaults{Thinking: tt.reasoning},
			}
			body, err := p.BuildRequest(
				schema.Model{ID: "m", Api: schema.ApiOpenAICompletions, Reasoning: true},
				schema.Context{},
				schema.StreamOptions{},
				profile,
			)
			require.NoError(t, err)

			var req map[string]any
			require.NoError(t, json.Unmarshal(body, &req))
			for k, v := range tt.want {
				assert.Equal(t, v, req[k])
			}
		})
	}
}
