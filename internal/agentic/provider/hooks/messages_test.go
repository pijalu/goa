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

// TestConvertThinkingToTextWhenRequired_EndpointRequiresReasoningEcho pins
// review finding F7: an endpoint whose profile REQUIRES reasoning_content on
// assistant messages is asking for the native thinking field, so the hook must
// not fold the thinking into visible text and leave reasoning_content empty.
// The opencode gateways do exactly that (requires_reasoning_content_on_assistant_messages:
// true, live-probed: their CatJam upstream answers 400
// missing_required_parameter messages[1].reasoning_content when the echo is
// absent), and models.dev declares the same requirement per model through
// interleaved.field.
func TestConvertThinkingToTextWhenRequired_EndpointRequiresReasoningEcho(t *testing.T) {
	msgs := []schema.Message{schema.NewAssistantMessage([]schema.ContentBlock{
		{Type: schema.ContentBlockThinking, Thinking: "I think..."},
	})}

	out := convertThinkingToTextWhenRequired(msgs, schema.Model{
		ID:             "cat-jam-free",
		Reasoning:      true,
		ThinkingFormat: schema.ThinkingFormatReasoningContent,
	}, schema.VariantProfile{Compat: schema.CompatFlags{
		ThinkingFormat: "openai",
		RequiresReasoningContentOnAssistantMessages: true,
	}})

	require.Len(t, out[0].Content, 1)
	assert.Equal(t, schema.ContentBlockThinking, out[0].Content[0].Type,
		"thinking must stay native when the endpoint requires the reasoning_content echo")

	// Same model, endpoint that does NOT ask for the echo: text is the safe
	// shape (status quo for OpenAI-class targets).
	out2 := convertThinkingToTextWhenRequired(msgs, schema.Model{
		ID:             "cat-jam-free",
		Reasoning:      true,
		ThinkingFormat: schema.ThinkingFormatReasoningContent,
	}, schema.VariantProfile{Compat: schema.CompatFlags{ThinkingFormat: "openai"}})

	require.Len(t, out2[0].Content, 1)
	assert.Equal(t, schema.ContentBlockText, out2[0].Content[0].Type)
}
