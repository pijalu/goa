// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"github.com/pijalu/goa/internal/agentic/provider"
)

// testAPIType is a unique API type used by tests to avoid conflicts with real providers.
const testAPIType provider.Api = "test-mock-api-"

// init registers a mock API provider so tests can create agents without real servers.
func init() {
	provider.RegisterApiProvider(&mockTestApiProvider{})
}

// mockTestApiProvider implements provider.ApiProvider and returns canned responses.
type mockTestApiProvider struct{}

func (m *mockTestApiProvider) API() provider.Api {
	return testAPIType
}

func (m *mockTestApiProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.Push(provider.AssistantMessageEvent{
			Type:         provider.EventTextStart,
			ContentIndex: 0,
		})
		result.Push(provider.AssistantMessageEvent{
			Type:         provider.EventTextDelta,
			ContentIndex: 0,
			Delta:        "Test response from mock provider.",
		})
		result.Push(provider.AssistantMessageEvent{
			Type:         provider.EventTextEnd,
			ContentIndex: 0,
		})
		result.End(&provider.AssistantMessage{
			Content: []provider.ContentBlock{
				{Type: provider.ContentBlockText, Text: "Test response from mock provider."},
			},
		})
	}()
	return result, nil
}

func (m *mockTestApiProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return m.Stream(model, ctx, base)
}

// testModel creates a minimal valid provider.Model for testing with the mock API provider.
func testModel(name string) provider.Model {
	return provider.Model{
		ID:         name,
		Name:       name,
		Api:        testAPIType,
		Provider:   provider.ProviderCustom,
		InputTypes: []string{"text"},
		BaseURL:    "http://localhost:9999/v1/chat/completions",
	}
}
