// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package models

import (
	"encoding/json"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// reasoningFixture covers the three shapes models.dev uses for a model's
// reasoning metadata. Review finding F4: the importer read only name/tool_call/
// reasoning/limit/cost, so a model's declared effort values and its
// interleaved reasoning field never reached the registry — cat-jam-free arrived
// from the catalog invisible (export goa-export-20261013-220109).
const reasoningFixture = `{
  "zai": {
    "models": {
      "effort-model": {
        "name": "Effort Model",
        "tool_call": true,
        "reasoning": true,
        "reasoning_options": [{"type": "effort", "values": ["low", "high", "max"]}],
        "interleaved": {"field": "reasoning_content"}
      },
      "toggle-model": {
        "name": "Toggle Model",
        "tool_call": true,
        "reasoning": true,
        "reasoning_options": [{"type": "toggle"}],
        "interleaved": true
      },
      "budget-model": {
        "name": "Budget Model",
        "tool_call": true,
        "reasoning": true,
        "reasoning_options": [{"type": "budget_tokens", "min": 0, "max": 31999}]
      },
      "plain-model": {
        "name": "Plain Model",
        "tool_call": true
      }
    }
  }
}`

func fixtureModels(t *testing.T, raw string) map[string]provider.Model {
	t.Helper()
	cat, err := parseModelsDev([]byte(raw))
	require.NoError(t, err)
	out := map[string]provider.Model{}
	for _, byID := range cat.byProv {
		for _, m := range byID {
			out[m.ID] = m
		}
	}
	return out
}

func TestConvertModelsDevModel_ImportsReasoningMetadata(t *testing.T) {
	models := fixtureModels(t, reasoningFixture)
	require.Len(t, models, 4)

	t.Run("effort values become an identity level map", func(t *testing.T) {
		m := models["effort-model"]
		assert.True(t, m.Reasoning)
		assert.Equal(t, provider.ThinkingLevelMap{
			provider.ThinkingLow:  "low",
			provider.ThinkingHigh: "high",
			provider.ThinkingMax:  "max",
		}, m.ThinkingLevelMap,
			"declared effort values must reach the registry so a level the model does not accept clamps instead of 400ing")
	})

	t.Run("interleaved field names the replay format", func(t *testing.T) {
		assert.Equal(t, provider.ThinkingFormatReasoningContent, models["effort-model"].ThinkingFormat)
	})

	t.Run("bare interleaved true decodes without inventing a format", func(t *testing.T) {
		m := models["toggle-model"]
		assert.Empty(t, m.ThinkingFormat,
			"a bool says the model interleaves, not which field it wants back")
		assert.Nil(t, m.ThinkingLevelMap, "toggle states on/off and names no levels")
	})

	t.Run("budget_tokens is a different domain", func(t *testing.T) {
		assert.Nil(t, models["budget-model"].ThinkingLevelMap)
	})

	t.Run("a model without reasoning metadata is untouched", func(t *testing.T) {
		m := models["plain-model"]
		assert.False(t, m.Reasoning)
		assert.Empty(t, m.ThinkingFormat)
		assert.Nil(t, m.ThinkingLevelMap)
	})
}

// TestParseModelsDev_UnknownMetadataDoesNotDropTheProvider pins the leniency
// these decoders exist for: the catalog is upstream data, and one unrecognized
// shape must not take a whole provider's models with it.
func TestParseModelsDev_UnknownMetadataDoesNotDropTheProvider(t *testing.T) {
	const fixture = `{
	  "zai": {
	    "models": {
	      "odd-model": {
	        "name": "Odd Model",
	        "tool_call": true,
	        "reasoning": true,
	        "interleaved": {"field": {"nested": "unexpected"}},
	        "reasoning_options": [{"type": "effort", "values": [1, 2, {"x": 1}, "high"]}]
	      },
	      "normal-model": {"name": "Normal Model", "tool_call": true, "reasoning": true}
	    }
	  }
	}`

	models := fixtureModels(t, fixture)
	assert.Len(t, models, 2, "an odd metadata shape must not drop its provider")
	assert.Equal(t, provider.ThinkingLevelMap{provider.ThinkingHigh: "high"},
		models["odd-model"].ThinkingLevelMap, "string values survive, the rest are dropped")
}

// TestReasoningOptionLevelMap_OnlyCanonicalLevels keeps the mapping honest:
// values the canonical vocabulary does not know ("none", "medium-low") are not
// invented as levels.
func TestReasoningOptionLevelMap_OnlyCanonicalLevels(t *testing.T) {
	got := reasoningOptionLevelMap([]mdReasoningOption{
		{Type: "effort", Values: []string{"none", "high", "very-high"}},
		{Type: "toggle"},
	}, true)

	assert.Equal(t, provider.ThinkingLevelMap{provider.ThinkingHigh: "high"}, got)
	assert.Nil(t, reasoningOptionLevelMap(nil, false), "a non-reasoning model gets no map")
}

// TestThinkingFormatFromInterleaved_UnknownFieldIgnored pins the deliberate
// silence: "reasoning_details" has no replay path in Goa, so it must not be
// reported as a format Goa would then claim to handle.
func TestThinkingFormatFromInterleaved_UnknownFieldIgnored(t *testing.T) {
	assert.Equal(t, provider.ThinkingFormatReasoningContent,
		thinkingFormatFromInterleaved(&mdInterleaved{Field: "reasoning_content"}))
	assert.Empty(t, thinkingFormatFromInterleaved(&mdInterleaved{Field: "reasoning_details"}))
	assert.Empty(t, thinkingFormatFromInterleaved(nil))
}

// TestMdInterleaved_DecodesBothShapes documents the polymorphism directly.
func TestMdInterleaved_DecodesBothShapes(t *testing.T) {
	var object mdInterleaved
	require.NoError(t, json.Unmarshal([]byte(`{"field":"reasoning_content"}`), &object))
	assert.Equal(t, "reasoning_content", object.Field)

	var boolValue mdInterleaved
	require.NoError(t, json.Unmarshal([]byte(`true`), &boolValue))
	assert.Empty(t, boolValue.Field)
}

// TestEmbeddedSnapshot_CarriesReasoningMetadata guards the offline floor: the
// committed api.json is what a first run without network uses, so the slimmer
// must carry the reasoning metadata for the catalog at large. Thresholds are
// deliberately loose (upstream churns constantly) — the point is that the
// fields are populated in bulk, which they are not if the slimmer drops them.
func TestEmbeddedSnapshot_CarriesReasoningMetadata(t *testing.T) {
	cat, err := parseModelsDev(embeddedAPIJSON)
	require.NoError(t, err)

	var interleaved, effort int
	for _, byID := range cat.byProv {
		for _, m := range byID {
			if m.ThinkingFormat == provider.ThinkingFormatReasoningContent {
				interleaved++
			}
			if len(m.ThinkingLevelMap) > 0 {
				effort++
			}
		}
	}

	assert.Greater(t, interleaved, 100,
		"the embedded snapshot must record the reasoning replay field for the models that declare one")
	assert.Greater(t, effort, 100,
		"the embedded snapshot must record the accepted effort values for the models that declare them")
}
