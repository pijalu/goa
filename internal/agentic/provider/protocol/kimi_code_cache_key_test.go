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

// kimiCodeModel mirrors the catalog kimi-code entry on the live protocol
// wire path (provider stream() dispatches ApiOpenAICompletions here, not to
// the legacy provider/openai builder).
var kimiCodeModel = schema.Model{
	ID:       "kimi-for-coding",
	Api:      schema.ApiOpenAICompletions,
	Provider: schema.ProviderKimiCode,
	BaseURL:  "https://api.kimi.com/coding/v1",
}

func buildWireBody(t *testing.T, model schema.Model, ctx schema.Context, opts schema.StreamOptions) map[string]any {
	t.Helper()
	profile := schema.ResolveProfile(model)
	body, err := ForAPI(model.Api).BuildRequest(model, ctx, opts, profile)
	require.NoError(t, err)
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	return req
}

// TestKimiCodeWire_SendsPromptCacheKeyAtDefaultRetention is the end-to-end
// regression for the kimi cache-affinity feature: with the default (none)
// cache retention the kimi-code profile must still emit the session identity
// as prompt_cache_key, matching kimi-code's unconditional
// generationKwargs.prompt_cache_key. The protocol layer resolves
// SupportsPromptCache from the embedded variant profile — when that profile
// carried supports_prompt_cache:false the wire body silently dropped the key
// even though the provider catalog opted in.
func TestKimiCodeWire_SendsPromptCacheKeyAtDefaultRetention(t *testing.T) {
	ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}
	opts := schema.StreamOptions{SessionID: "kimi-session", CacheRetention: schema.CacheRetentionNone}
	req := buildWireBody(t, kimiCodeModel, ctx, opts)
	assert.Equal(t, "kimi-session", req["prompt_cache_key"],
		"kimi-code must send the session cache identity at default retention")
}

// TestMoonshotWire_SendsPromptCacheKeyAtDefaultRetention covers the plain
// Moonshot profile, which shares the same catalog/profile flag.
func TestMoonshotWire_SendsPromptCacheKeyAtDefaultRetention(t *testing.T) {
	model := schema.Model{
		ID:       "kimi-k2.6",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderKimi,
		BaseURL:  "https://api.moonshot.cn/v1",
	}
	ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}
	opts := schema.StreamOptions{SessionID: "kimi-session", CacheRetention: schema.CacheRetentionNone}
	req := buildWireBody(t, model, ctx, opts)
	assert.Equal(t, "kimi-session", req["prompt_cache_key"],
		"moonshot must send the session cache identity at default retention")
}

// TestKimiCodeWire_PromptCacheKeyStableAcrossRequestVariants walks one
// conversation through the three request shapes of an agentic turn — normal
// tool round, follow-up after a tool result, final NoTools collapse — and
// asserts the same prompt_cache_key on every wire request (kimi-code
// preserves the session key across all requests of a session).
func TestKimiCodeWire_PromptCacheKeyStableAcrossRequestVariants(t *testing.T) {
	tool := schema.ToolSchema{
		Name:        "read",
		Description: "read a file",
		InputSchema: map[string]any{"type": "object"},
	}
	opts := schema.StreamOptions{SessionID: "kimi-session", CacheRetention: schema.CacheRetentionNone}

	user := schema.NewUserMessage("read README")
	assistantCall := schema.NewAssistantMessage([]schema.ContentBlock{{
		Type:          schema.ContentBlockToolCall,
		ToolCallID:    "call-1",
		ToolName:      "read",
		ToolArguments: `{"path":"README.md"}`,
	}})
	toolResult := schema.NewToolResultMessage("call-1", "read", "contents", false)

	steps := []struct {
		name string
		ctx  schema.Context
	}{
		{"initial tool round", schema.Context{Messages: []schema.Message{user}, Tools: []schema.ToolSchema{tool}}},
		{"after tool result", schema.Context{Messages: []schema.Message{user, assistantCall, toolResult}, Tools: []schema.ToolSchema{tool}}},
		{"final no-tools collapse", schema.Context{Messages: []schema.Message{user, assistantCall, toolResult}, NoTools: true}},
	}
	for _, step := range steps {
		req := buildWireBody(t, kimiCodeModel, step.ctx, opts)
		assert.Equal(t, "kimi-session", req["prompt_cache_key"], "%s must keep the session cache key", step.name)
	}
}

// TestKimiCodeWire_ExplicitPromptCacheKeyWins verifies the dedicated cache
// key takes precedence over the session ID, matching promptCacheIdentity.
func TestKimiCodeWire_ExplicitPromptCacheKeyWins(t *testing.T) {
	ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}
	opts := schema.StreamOptions{
		SessionID:      "kimi-session",
		PromptCacheKey: "dedicated-key",
		CacheRetention: schema.CacheRetentionNone,
	}
	req := buildWireBody(t, kimiCodeModel, ctx, opts)
	assert.Equal(t, "dedicated-key", req["prompt_cache_key"])
}

// TestWire_PromptCacheKeyStillGatedForUnflaggedProviders guards the other
// side of the gate: providers without the prompt-cache flag keep omitting
// the key at default retention (strict endpoints 400 on unknown fields).
func TestWire_PromptCacheKeyStillGatedForUnflaggedProviders(t *testing.T) {
	model := schema.Model{
		ID:       "deepseek-chat",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderDeepSeek,
		BaseURL:  "https://api.deepseek.com/v1",
	}
	ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}
	opts := schema.StreamOptions{SessionID: "ds-session", CacheRetention: schema.CacheRetentionNone}
	req := buildWireBody(t, model, ctx, opts)
	_, present := req["prompt_cache_key"]
	assert.False(t, present, "unflagged providers must not receive prompt_cache_key at default retention")
}
