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

// Payload-level acceptance tests for bugs.md #4 — "No max_tokens cap on
// reasoning-capable providers": reasoning and the answer share one output
// budget (pi, packages/ai/src/types.ts:834-840), so a request with no cap can
// spend the whole response on reasoning and emit no answer. The z.ai export
// that motivated the report sent no cap at all (thinking_level xhigh,
// max_tokens 0) and burned 4268 reasoning tokens before its first tool call.

func reasoningZaiModel() schema.Model {
	return schema.Model{
		ID:            "glm-5.2",
		Api:           schema.ApiOpenAICompletions,
		Provider:      schema.ProviderZai,
		BaseURL:       "https://api.z.ai/api/coding",
		Reasoning:     true,
		MaxTokens:     131072, // models.dev zai limit.output
		ContextWindow: 1000000,
	}
}

// TestMaxTokens_ReasoningModelCappedAtOutputCeiling is the bugs.md #4 core
// assertion: a z.ai reasoning model with no configured cap still sends an
// explicit output cap, materialized in the provider's MaxTokensField
// (max_tokens for z.ai) at the model's known output ceiling.
func TestMaxTokens_ReasoningModelCappedAtOutputCeiling(t *testing.T) {
	req := buildOpenAIRequest(t, reasoningZaiModel(), schema.StreamOptions{})
	assert.Equal(t, float64(131072), req["max_tokens"],
		"reasoning-capable z.ai request must carry an explicit output cap")
	_, hasCompletionTokens := req["max_completion_tokens"]
	assert.False(t, hasCompletionTokens,
		"the cap must use the provider's MaxTokensField (max_tokens for z.ai)")
}

// TestMaxTokens_ExplicitUserCapWins pins "skip when the user set an explicit
// value": model.max_tokens in the config overrides every default.
func TestMaxTokens_ExplicitUserCapWins(t *testing.T) {
	req := buildOpenAIRequest(t, reasoningZaiModel(), schema.StreamOptions{MaxTokens: 4096})
	assert.Equal(t, float64(4096), req["max_tokens"],
		"an explicit user cap must win over the reasoning ceiling")
}

// TestMaxTokens_CatalogDefaultBeatsReasoningCeiling keeps the P21 precedence
// intact: the provider catalog's default_max_tokens still outranks the derived
// ceiling.
func TestMaxTokens_CatalogDefaultBeatsReasoningCeiling(t *testing.T) {
	model := schema.Model{
		ID:        "deepseek-v4-flash",
		Api:       schema.ApiOpenAICompletions,
		Provider:  schema.ProviderDeepSeek,
		BaseURL:   "https://api.deepseek.com",
		Reasoning: true,
		MaxTokens: 131072,
	}
	req := buildOpenAIRequest(t, model, schema.StreamOptions{})
	assert.Equal(t, float64(256000), req["max_tokens"],
		"catalog default_max_tokens must outrank the reasoning ceiling")
}

// TestMaxTokens_ReasoningCeilingUnknownFallsBack covers a reasoning model whose
// output ceiling the catalog does not know: the request is still capped rather
// than left unbounded.
func TestMaxTokens_ReasoningCeilingUnknownFallsBack(t *testing.T) {
	model := reasoningZaiModel()
	model.MaxTokens = 0
	req := buildOpenAIRequest(t, model, schema.StreamOptions{})
	assert.Equal(t, float64(FallbackReasoningMaxTokens), req["max_tokens"],
		"a reasoning model with unknown ceiling must fall back to the safe cap")
}

// TestMaxTokens_NonReasoningProviderUnchanged is the bugs.md #4 guard: providers
// whose model is not reasoning-capable keep the historical behavior (no cap
// when neither the request nor the catalog sets one).
func TestMaxTokens_NonReasoningProviderUnchanged(t *testing.T) {
	model := schema.Model{
		ID:       "gpt-4o",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.ProviderOpenAI,
		BaseURL:  "https://api.openai.com/v1",
	}
	req := buildOpenAIRequest(t, model, schema.StreamOptions{})
	_, ok := req["max_completion_tokens"]
	assert.False(t, ok, "non-reasoning provider without catalog default must stay uncapped")
	_, ok = req["max_tokens"]
	assert.False(t, ok, "non-reasoning provider without catalog default must stay uncapped")
}

// TestMaxTokens_RespectsMaxTokensFieldCompat asserts the cap rides the
// configured MaxTokensField rather than a hardcoded key: this route declares
// no override, so it must fall back to max_completion_tokens.
func TestMaxTokens_RespectsMaxTokensFieldCompat(t *testing.T) {
	model := schema.Model{
		ID:        "reasoning-model",
		Api:       schema.ApiOpenAICompletions,
		Provider:  schema.ProviderTogether,
		BaseURL:   "https://api.together.xyz/v1",
		Reasoning: true,
		MaxTokens: 8192,
	}
	profile := schema.ResolveProfile(model)
	body, err := ForAPI(schema.ApiOpenAICompletions).BuildRequest(model, schema.Context{
		Messages: []schema.Message{schema.NewUserMessage("hi")},
	}, schema.StreamOptions{}, profile)
	require.NoError(t, err)
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))

	compat := resolveOpenAICompat(model, profile)
	require.NotEmpty(t, compat.MaxTokensField, "a completions route must name its cap field")
	assert.Equal(t, float64(8192), req[compat.MaxTokensField],
		"the reasoning cap must be sent in the configured MaxTokensField")
}
