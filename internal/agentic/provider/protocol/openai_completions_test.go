// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package protocol

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestResolveOpenAICompat_ReasoningContentFlagPropagation is the regression
// test for the thinking-mode 400 (bugs.md): the opencode variant profile
// carries requires_reasoning_content_on_assistant_messages=true, but
// resolveOpenAICompat dropped it, so proxied DeepSeek models 400'd with
// "reasoning_content in the thinking mode must be passed back".
func TestResolveOpenAICompat_ReasoningContentFlagPropagation(t *testing.T) {
	profile := schema.VariantProfile{
		Compat: schema.CompatFlags{
			RequiresReasoningContentOnAssistantMessages: true,
		},
	}
	compat := resolveOpenAICompat(schema.Model{}, profile)
	assert.True(t, compat.RequiresReasoningContentOnAssistantMessages,
		"profile flag must reach the serializer compat")

	compat = resolveOpenAICompat(schema.Model{}, schema.VariantProfile{})
	assert.False(t, compat.RequiresReasoningContentOnAssistantMessages,
		"flag defaults off without a profile requirement")
}

// TestConvertAssistantMessage_ReasoningContent pins both serialization
// behaviors: thinking blocks always serialize as reasoning_content; the flag
// additionally injects an empty reasoning_content on assistant messages
// without thinking (DeepSeek requires the key on EVERY assistant message in
// thinking mode); without the flag the key stays absent.
func TestConvertAssistantMessage_ReasoningContent(t *testing.T) {
	thinkingMsg := schema.Message{
		Role: schema.RoleAssistant,
		Content: []schema.ContentBlock{
			{Type: schema.ContentBlockThinking, Thinking: "chain of thought"},
			{Type: schema.ContentBlockText, Text: "answer"},
		},
	}
	plainMsg := schema.Message{
		Role:    schema.RoleAssistant,
		Content: []schema.ContentBlock{{Type: schema.ContentBlockText, Text: "answer"}},
	}

	// Thinking always serializes, flag or not.
	out := convertAssistantMessage(thinkingMsg, openAICompletionsCompat{})
	assert.Equal(t, "chain of thought", out["reasoning_content"])

	// Flag on: plain assistant message gets an empty reasoning_content key.
	out = convertAssistantMessage(plainMsg, openAICompletionsCompat{RequiresReasoningContentOnAssistantMessages: true})
	rc, ok := out["reasoning_content"]
	require.True(t, ok, "flag on: reasoning_content key must be present")
	assert.Equal(t, "", rc)

	// Flag off: the key stays absent (pinned — providers that reject unknown
	// fields rely on this).
	out = convertAssistantMessage(plainMsg, openAICompletionsCompat{})
	_, ok = out["reasoning_content"]
	assert.False(t, ok, "flag off: reasoning_content must be absent")
}

// TestResolveProfile_OpencodeCarriesReasoningContentFlag verifies the
// embedded opencode variant (zen proxy, DeepSeek-class models) resolves with
// the requirement set for a proxied deepseek model id unknown to the models
// registry.
func TestResolveProfile_OpencodeCarriesReasoningContentFlag(t *testing.T) {
	profile := schema.ResolveProfile(schema.Model{
		ID:       "deepseek-v4-flash-free",
		Api:      schema.ApiOpenAICompletions,
		Provider: schema.Provider("opencode"),
		BaseURL:  "https://opencode.ai/zen",
	})
	assert.True(t, profile.Compat.RequiresReasoningContentOnAssistantMessages,
		"embedded opencode variant must require reasoning_content passback")
}
