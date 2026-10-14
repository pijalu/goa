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

// catJamModel mirrors how the manager mints the manually-added cat-jam-free on
// the OpenCode Go gateway when the model is absent from the catalog: the
// fallback path defaults Reasoning to true (provider/manager_fallback.go).
func catJamModel() schema.Model {
	return schema.Model{
		ID:        "cat-jam-free",
		Api:       schema.ApiOpenAICompletions,
		Provider:  schema.ProviderOpenCodeGo,
		BaseURL:   "https://opencode.ai/zen/go/v1",
		Reasoning: true,
	}
}

func catJamBody(t *testing.T, model schema.Model, opts schema.StreamOptions) map[string]any {
	t.Helper()
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)
	ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("teqst")}}
	body, err := p.BuildRequest(model, ctx, opts, schema.ResolveProfile(model))
	require.NoError(t, err)
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	return req
}

// Regression for exports goa-export-20261013-220109 and -222008: cat-jam-free on
// opencode-go failed every turn with
//
//	HTTP 400 {"error":{"param":"thinking","type":"invalid_request_error",
//	          "message":"Upstream request failed: [unsupported_parameter]
//	          Unrecognized request argument supplied: thinking."}}
//
// The upstream (zen "CatJam") is deepseek-class — it requires the
// reasoning_content echo (opencode-go profile: requires_reasoning_content_
// on_assistant_messages) and takes reasoning_effort — but it ignores no unknown
// arguments, so the DeepSeek direct-API "thinking" object the zen profiles used
// to send was fatal. The reference client (opencode, npm
// "@ai-sdk/openai-compatible" → openaiChat) never sends that object here.
func TestCatJamFreeGoGatewaySendsNoThinkingObject(t *testing.T) {
	profile := schema.ResolveProfile(catJamModel())
	assert.Equal(t, "openai", string(profile.Compat.ThinkingFormat),
		"zen/go must use the OpenAI-compatible effort body")
	assert.True(t, profile.Compat.RequiresReasoningContentOnAssistantMessages,
		"the reasoning_content echo requirement must stay (probed 2026-10-13)")

	for _, tc := range []struct {
		name string
		opts schema.StreamOptions
		want any
	}{
		{"no level configured (main-agent default)", schema.StreamOptions{}, "medium"},
		{"level high", schema.StreamOptions{Reasoning: schema.ThinkingHigh}, "high"},
		{"level off", schema.StreamOptions{Reasoning: schema.ThinkingOff}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			req := catJamBody(t, catJamModel(), tc.opts)
			assert.NotContains(t, req, "thinking",
				"the strict upstream rejects thinking in every spelling")
			if tc.want == nil {
				assert.NotContains(t, req, "reasoning_effort")
				return
			}
			assert.Equal(t, tc.want, req["reasoning_effort"])
		})
	}
}

// The sibling zen/v1 surface (provider "opencode") serves the same model zoo
// through the same OpenAI-compatible protocol, so it must not regress either.
func TestCatJamFreeZenSurfaceSendsNoThinkingObject(t *testing.T) {
	model := catJamModel()
	model.Provider = schema.ProviderOpenCode
	model.BaseURL = "https://opencode.ai/zen/v1"
	req := catJamBody(t, model, schema.StreamOptions{})
	assert.NotContains(t, req, "thinking")
	assert.Equal(t, "medium", req["reasoning_effort"])
}

// A model with an explicit thinking_level_native_map (the user's cat-jam-free
// config) keeps its level translation on top of the effort-only body: the map
// only renames the level, it never reintroduces the thinking object.
func TestCatJamFreeNativeLevelMapKeepsEffortOnlyBody(t *testing.T) {
	model := catJamModel()
	model.ThinkingLevelMap = schema.ThinkingLevelMap{
		schema.ThinkingOff:     "off",
		schema.ThinkingMinimal: "minimal",
		schema.ThinkingLow:     "low",
		schema.ThinkingMedium:  "medium",
		schema.ThinkingHigh:    "high",
		schema.ThinkingXHigh:   "xhigh",
	}
	req := catJamBody(t, model, schema.StreamOptions{Reasoning: schema.ThinkingXHigh})
	assert.NotContains(t, req, "thinking")
	assert.Equal(t, "xhigh", req["reasoning_effort"])
}
