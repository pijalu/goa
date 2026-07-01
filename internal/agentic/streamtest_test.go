// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"fmt"
	"sync/atomic"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// testAPIProvider is a configurable mock API provider for tests.
// It returns predetermined events via the Stream API.
type testAPIProvider struct {
	api    provider.Api
	events []provider.AssistantMessageEvent
	// usage, when non-nil, is attached to the terminal AssistantMessage result,
	// simulating a provider that reports token usage (e.g. via stream_options).
	usage     *provider.Usage
	exhausted atomic.Bool
}

func (p *testAPIProvider) API() provider.Api {
	return provider.Api(p.api)
}

func (p *testAPIProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	// On re-stream (tool calls encountered), push a plain text response
	// instead of the predetermined tool-call events, to avoid infinite
	// tool-call loops in tests.
	extraRound := p.exhausted.Swap(true)
	go func() {
		if extraRound {
			result.Push(provider.AssistantMessageEvent{
				Type:  provider.EventTextDelta,
				Delta: "Done processing tool calls.",
			})
			result.End(&provider.AssistantMessage{
				Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "Done processing tool calls."}},
				StopReason: provider.StopReasonEndTurn,
				Usage:      p.usage,
			})
			return
		}
		for _, event := range p.events {
			result.Push(event)
		}
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "mock done"}},
			StopReason: provider.StopReasonEndTurn,
			Usage:      p.usage,
		})
	}()
	return result, nil
}

func (p *testAPIProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

// testModel creates a minimal provider.Model for testing with the given API type.
func testModel(api provider.Api) provider.Model {
	return provider.Model{
		ID:         "test-model",
		Name:       "test-model",
		Api:        api,
		Provider:   provider.ProviderCustom,
		InputTypes: []string{"text"},
	}
}

var testProviderCounter atomic.Int64

// registerTestProvider registers and returns a test API provider.
// Each call generates a unique API type to avoid duplicate registration panics.
func registerTestProvider(name string, events []provider.AssistantMessageEvent) *testAPIProvider {
	uniqueID := testProviderCounter.Add(1)
	p := &testAPIProvider{
		api:    provider.Api(fmt.Sprintf("test-%s-%d", name, uniqueID)),
		events: events,
	}
	provider.RegisterApiProvider(p)
	return p
}
