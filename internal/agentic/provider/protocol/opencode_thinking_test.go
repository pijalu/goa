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

// opencodeModel builds the fallback-path model for the manually-added
// x-preview-f-free: absent from the registry, Reasoning=true by default.
func opencodeModel() schema.Model {
	return schema.Model{
		ID: "x-preview-f-free", Api: schema.ApiOpenAICompletions,
		Provider: schema.ProviderOpenCode, Reasoning: true,
	}
}

func buildOpenCodeBody(t *testing.T, model schema.Model, opts schema.StreamOptions) map[string]any {
	t.Helper()
	p := ForAPI(schema.ApiOpenAICompletions)
	require.NotNil(t, p)
	profile := schema.ResolveProfile(model)
	require.Equal(t, "openai", string(profile.Compat.ThinkingFormat),
		"the zen gateways control reasoning with reasoning_effort, not a thinking object")
	ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}
	body, err := p.BuildRequest(model, ctx, opts, profile)
	require.NoError(t, err)
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	return req
}

// The OpenCode Zen gateways (zen/v1 and zen/go) are OpenAI-compatible surfaces:
// models.dev declares them with npm "@ai-sdk/openai-compatible", which the
// reference client maps to its openaiChat protocol — reasoning is controlled by
// reasoning_effort alone (opencode packages/core/src/variant.ts openaiChat) and
// no "thinking" object is ever sent. The profile must therefore use the
// "openai" body, not the DeepSeek direct-API body: zen proxies strict upstreams
// (e.g. CatJam/"cat-jam-free") that reject the unknown argument outright with
// HTTP 400 "[unsupported_parameter] Unrecognized request argument supplied:
// thinking." — the failure in exports 20261013-220109 / 20261013-222008.
func TestOpenCodeThinkingBodyIsEffortOnly(t *testing.T) {
	req := buildOpenCodeBody(t, opencodeModel(), schema.StreamOptions{Reasoning: schema.ThinkingHigh})
	assert.NotContains(t, req, "thinking",
		"zen upstreams reject the thinking object; only reasoning_effort is understood")
	assert.Equal(t, "high", req["reasoning_effort"],
		"the configured level must reach the wire")
}

// With no explicit level the fallback default remains medium: it is the value
// the gateway accepts for models whose declared efforts omit it (live probe
// 2026-10-13: reasoning_effort "medium" → 200 on cat-jam-free and
// deepseek-v4-flash, whose models.dev effort lists are low/medium/high/xhigh/max
// and low/high/max respectively).
func TestOpenCodeFallbackModelNoLevelDefaultsToMedium(t *testing.T) {
	req := buildOpenCodeBody(t, opencodeModel(), schema.StreamOptions{})
	assert.Equal(t, "medium", req["reasoning_effort"],
		"with no configured level the fallback default remains medium")
	assert.NotContains(t, req, "thinking")
}

// Regression for the x-preview-f-free HTTP 400 ("always engages in
// thinking... use low, high, or max"): once the main-agent path plumbs the
// configured thinking level into StreamOptions.Reasoning, a native-accepted
// level must reach the wire verbatim (no map defined).
func TestOpenCodeFallbackModelLevelReachesWire(t *testing.T) {
	req := buildOpenCodeBody(t, opencodeModel(), schema.StreamOptions{Reasoning: schema.ThinkingHigh})
	assert.Equal(t, "high", req["reasoning_effort"],
		"the configured level must reach the wire, not be silently replaced by medium")
}

// The direct per-model escape hatch: a config thinking_level_native_map maps
// the canonical level to the provider-native value the always-thinking model
// accepts (xhigh -> max). Model.ThinkingLevelMap is the migration bridge that
// schema.ResolveProfile copies into the profile when the profile has no map.
func TestOpenCodeFallbackModelNativeMapOverridesLevel(t *testing.T) {
	model := opencodeModel()
	model.ThinkingLevelMap = schema.ThinkingLevelMap{
		schema.ThinkingXHigh: "max",
		schema.ThinkingHigh:  "high",
		schema.ThinkingLow:   "low",
	}
	req := buildOpenCodeBody(t, model, schema.StreamOptions{Reasoning: schema.ThinkingXHigh})
	assert.Equal(t, "max", req["reasoning_effort"],
		"native map must translate xhigh -> max so the always-thinking model accepts it")
}

// Thinking off omits the reasoning parameters entirely on the zen surfaces: the
// "openai" body has no disabled spelling (thinkingDisabledBodyForFormat), so a
// server-side sticky default cannot be contradicted — and the removed thinking
// object is never reintroduced by the off path (live probe: thinking:{type:
// "disabled"} is rejected by the same strict upstreams as the enabled one).
func TestOpenCodeThinkingOffSendsNoThinkingBody(t *testing.T) {
	req := buildOpenCodeBody(t, opencodeModel(), schema.StreamOptions{Reasoning: schema.ThinkingOff})
	assert.NotContains(t, req, "thinking")
	assert.NotContains(t, req, "reasoning_effort")
}
