// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package hooks

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestConvertThinkingToTextWhenRequired(t *testing.T) {
	msgs := []schema.Message{schema.NewAssistantMessage([]schema.ContentBlock{
		{Type: schema.ContentBlockThinking, Thinking: "I think..."},
	})}

	// OpenAI source → no native thinking target (none) → converted to text
	out := convertThinkingToTextWhenRequired(msgs, schema.Model{
		Reasoning:      true,
		ThinkingFormat: schema.ThinkingFormatReasoningContent,
	}, schema.VariantProfile{Compat: schema.CompatFlags{ThinkingFormat: "none"}})
	require.Len(t, out[0].Content, 1)
	assert.Equal(t, schema.ContentBlockText, out[0].Content[0].Type)
	assert.Equal(t, "I think...", out[0].Content[0].Text)

	// Anthropic target supports thinking → kept
	out2 := convertThinkingToTextWhenRequired(msgs, schema.Model{
		Reasoning:      true,
		ThinkingFormat: schema.ThinkingFormatThinkingContent,
	}, schema.VariantProfile{Compat: schema.CompatFlags{ThinkingFormat: "thinking_content"}})
	require.Len(t, out2[0].Content, 1)
	assert.Equal(t, schema.ContentBlockThinking, out2[0].Content[0].Type)
}
