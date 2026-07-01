// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/protocol"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCrossProviderReplay(t *testing.T) {
	anthropicModel := schema.Model{
		ID:             "claude-sonnet-4-20250514",
		Api:            schema.ApiAnthropicMessages,
		Provider:       schema.ProviderAnthropic,
		Reasoning:      true,
		ThinkingFormat: schema.ThinkingFormatThinkingContent,
	}
	openaiModel := schema.Model{
		ID:             "gpt-4o",
		Api:            schema.ApiOpenAICompletions,
		Provider:       schema.ProviderOpenAI,
		Reasoning:      true,
		ThinkingFormat: schema.ThinkingFormatReasoningContent,
	}
	mistralModel := schema.Model{
		ID:             "mistral-large-2",
		Api:            schema.ApiMistralConversations,
		Provider:       schema.ProviderMistral,
		ThinkingFormat: schema.ThinkingFormatNone,
	}

	messages := []schema.Message{
		schema.NewUserMessage("hello"),
		schema.NewAssistantMessage([]schema.ContentBlock{
			{Type: schema.ContentBlockText, Text: "hi"},
			{Type: schema.ContentBlockThinking, Thinking: "thinking..."},
		}),
	}

	tests := []struct {
		name string
		from schema.Model
		to   schema.Model
	}{
		{"anthropic-to-openai", anthropicModel, openaiModel},
		{"openai-to-anthropic", openaiModel, anthropicModel},
		{"openai-to-mistral", openaiModel, mistralModel},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// TransformMessages is the legacy transform path; the new hook
			// pipeline also covers these cases.
			result := TransformMessages(messages, tt.to, nil)
			require.NotEmpty(t, result)

			// Verify the new hook pipeline can build a request for the target.
			p := protocol.ForAPI(tt.to.Api)
			require.NotNil(t, p)
			_, err := p.BuildRequest(tt.to, schema.Context{Messages: result}, schema.StreamOptions{}, schema.ResolveProfile(tt.to))
			require.NoError(t, err)
		})
	}
}

func TestGenericRuntimeForAllAPIs(t *testing.T) {
	for _, api := range protocol.RegisteredAPIs() {
		t.Run(string(api), func(t *testing.T) {
			model := schema.Model{
				ID:       "test-model",
				Api:      api,
				Provider: schema.ProviderCustom,
			}
			ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}
			stream, err := GenericStream(model, ctx, schema.StreamOptions{})
			require.NoError(t, err)
			assert.NotNil(t, stream)
			_ = stream.Result()
		})
	}
}
