// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package provider

import (
	"context"
	"errors"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider/protocol"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
	"github.com/pijalu/goa/internal/agentic/provider/transport"
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

// TestGenericRuntimeForAllAPIs walks every registered protocol through the
// generic pipeline. Each model carries an explicit endpoint: the runtime
// refuses to guess a host for a provider it cannot place (the empty-endpoint
// fallback to api.openai.com is what sent a gateway's namespaced model id to
// another vendor — bugs.md "Vercel AI Gateway"), so a URL must be given.
func TestGenericRuntimeForAllAPIs(t *testing.T) {
	old := transport.Default()
	defer transport.SetDefault(old)
	// The smoke test only needs every API to reach the wire: a fast-failing
	// transport terminates each stream instead of waiting on a parse of a body
	// shaped for a different protocol.
	transport.SetDefault(failingTransport{})

	for _, api := range protocol.RegisteredAPIs() {
		t.Run(string(api), func(t *testing.T) {
			model := schema.Model{
				ID:       "test-model",
				Api:      api,
				Provider: schema.ProviderCustom,
				BaseURL:  testEndpointForAPI(api),
			}
			ctx := schema.Context{Messages: []schema.Message{schema.NewUserMessage("hi")}}
			stream, err := GenericStream(model, ctx, schema.StreamOptions{})
			require.NoError(t, err)
			assert.NotNil(t, stream)
			_ = stream.Result()
		})
	}
}

// failingTransport fails every request immediately; the smoke test only needs
// the pipeline to build and dispatch a request for every API.
type failingTransport struct{}

func (failingTransport) Do(context.Context, *transport.TransportRequest) (*transport.TransportResponse, error) {
	return nil, errors.New("dial tcp 127.0.0.1:1: connect: connection refused (smoke test)")
}

// testEndpointForAPI returns an explicit endpoint for each wire API so the
// pipeline can resolve a request URL without consulting any catalog default.
func testEndpointForAPI(api schema.Api) string {
	switch api {
	case schema.ApiOpenAIResponses, schema.ApiAzureOpenAIResponses:
		return "http://127.0.0.1:1/v1/responses"
	case schema.ApiOpenAICodexResponses:
		return "http://127.0.0.1:1/codex/responses"
	case schema.ApiAnthropicMessages:
		return "http://127.0.0.1:1/v1/messages"
	case schema.ApiGoogleGenerativeAI:
		return "http://127.0.0.1:1/v1beta/models/test-model:streamGenerateContent?alt=sse"
	case schema.ApiBedrockConverse:
		return "http://127.0.0.1:1/model/test-model/converse-stream"
	default:
		return "http://127.0.0.1:1/v1/chat/completions"
	}
}
