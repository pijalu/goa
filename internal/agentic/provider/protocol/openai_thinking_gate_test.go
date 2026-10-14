// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
)

// TestApplyThinking_NonReasoningModelSendsNoReasoningField pins review finding
// F2: the reasoning gate in applyThinking must key on the model's capability,
// not on whether the profile happens to declare a thinking format. A provider
// that declares one (every gateway does) used to make the gate dead code, so a
// non-reasoning model still carried a reasoning body — the class of field a
// strict upstream rejects with 400 unsupported_parameter.
func TestApplyThinking_NonReasoningModelSendsNoReasoningField(t *testing.T) {
	nonReasoning := schema.Model{
		ID:        "some-chat-model",
		Api:       schema.ApiOpenAICompletions,
		Provider:  schema.ProviderOpenCodeGo,
		BaseURL:   "https://opencode.ai/zen/go/v1",
		Reasoning: false,
	}

	t.Run("no reasoning field without an explicit level", func(t *testing.T) {
		body := catJamBody(t, nonReasoning, schema.StreamOptions{APIKey: "k"})

		assert.NotContains(t, body, "thinking")
		assert.NotContains(t, body, "reasoning_effort")
	})

	t.Run("explicit level still reaches a catalog-lagged model", func(t *testing.T) {
		body := catJamBody(t, nonReasoning, schema.StreamOptions{
			APIKey:    "k",
			Reasoning: schema.ThinkingHigh,
		})

		assert.Equal(t, "high", body["reasoning_effort"])
	})

	t.Run("explicit off is still sent", func(t *testing.T) {
		body := catJamBody(t, nonReasoning, schema.StreamOptions{
			APIKey:    "k",
			Reasoning: schema.ThinkingOff,
		})

		assert.NotContains(t, body, "thinking")
	})

	t.Run("reasoning model keeps its body", func(t *testing.T) {
		body := catJamBody(t, catJamModel(), schema.StreamOptions{APIKey: "k"})

		assert.Equal(t, "medium", body["reasoning_effort"])
	})
}
