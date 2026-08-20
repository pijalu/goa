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
	require.Equal(t, "deepseek", string(profile.Compat.ThinkingFormat),
		"opencode profile must use the deepseek thinking format")
	ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}
	body, err := p.BuildRequest(model, ctx, opts, profile)
	require.NoError(t, err)
	var req map[string]any
	require.NoError(t, json.Unmarshal(body, &req))
	return req
}

// The opencode variant uses thinking_format "deepseek" but declares no
// thinking_level_map. With no explicit level and no map, the level still
// resolves to the "medium" fallback — this pins that default so the
// config-plumbing fix (which now always sends a level) is a deliberate change.
func TestOpenCodeFallbackModelNoLevelDefaultsToMedium(t *testing.T) {
	req := buildOpenCodeBody(t, opencodeModel(), schema.StreamOptions{})
	assert.Equal(t, "medium", req["reasoning_effort"],
		"with no configured level the fallback default remains medium")
}

// Regression for the x-preview-f-free HTTP 400 ("always engages in
// thinking... use low, high, or max"): once the main-agent path plumbs the
// configured thinking level into StreamOptions.Reasoning, a native-accepted
// level must reach the wire verbatim (no map defined).
func TestOpenCodeFallbackModelLevelReachesWire(t *testing.T) {
	req := buildOpenCodeBody(t, opencodeModel(), schema.StreamOptions{Reasoning: schema.ThinkingHigh})
	assert.Equal(t, "high", req["reasoning_effort"],
		"the configured level must reach the wire, not be silently replaced by medium")
	thinking, ok := req["thinking"].(map[string]any)
	require.True(t, ok, "deepseek format must send an explicit thinking body")
	assert.Equal(t, "enabled", thinking["type"])
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
