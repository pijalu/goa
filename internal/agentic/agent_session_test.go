// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestAgent_StreamsContent verifies content is streamed through the Output channel.
func TestAgent_StreamsContent(t *testing.T) {
	st := &streamTestProvider{api: "test-stream-api", events: []provider.AssistantMessageEvent{
		{Type: provider.EventTextStart, ContentIndex: 0},
		{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "Hello "},
		{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "World"},
		{Type: provider.EventTextEnd, ContentIndex: 0},
	}}
	provider.RegisterApiProvider(st)

	done := make(chan struct{})
	obs := &mockEventObserver{}
	obs.OnEvent(OutputEvent{}) // dummy to init

	agent := NewAgent(Config{
		Model: provider.Model{
			ID:         "stream-test",
			Api:        st.API(),
			Provider:   provider.ProviderCustom,
			InputTypes: []string{"text"},
		},
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
	})
	agent.AddObserver(obs)

	ctx := context.Background()
	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx, "Hi")
		close(done)
	}()

	// Drain output in background
	go func() {
		for range agent.Output {
		}
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for agent")
	}
	agent.Stop()
	<-done
}

// TestAgent_ExecutesTool_Stream verifies tool execution in the new stream-based path.
func TestAgent_ExecutesTool_Stream(t *testing.T) {
	st2 := &streamTestProvider{api: "test-tool-api", events: []provider.AssistantMessageEvent{
		{Type: provider.EventToolCallEnd, ToolCall: &provider.ContentBlock{
			Type: provider.ContentBlockToolCall, ToolName: "calculator", ToolArguments: `{"a":10,"b":20,"op":"+"}`,
		}},
	}}
	provider.RegisterApiProvider(st2)

	calcTool := mockTool{
		name: "calculator",
		schema: ToolSchema{
			Name:        "calculator",
			Description: "math operations",
			Schema: map[string]interface{}{
				"type": "object",
				"properties": map[string]interface{}{
					"a":  map[string]string{"type": "number"},
					"b":  map[string]string{"type": "number"},
					"op": map[string]string{"type": "string"},
				},
				"required": []string{"a", "b", "op"},
			},
		},
	}

	agent := NewAgent(Config{
		Model: provider.Model{
			ID:         "tool-test",
			Api:        st2.API(),
			Provider:   provider.ProviderCustom,
			InputTypes: []string{"text"},
		},
		SystemPrompt: "You are helpful",
		Logger:       NewLogger(Error),
		Tools:        []Tool{calcTool},
	})

	ctx := context.Background()
	errCh := make(chan error, 1)
	go func() {
		errCh <- agent.Run(ctx, "Calculate 10+20")
	}()

	// Drain output in background
	go func() {
		for range agent.Output {
		}
	}()

	select {
	case err := <-errCh:
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout waiting for agent")
	}
	agent.Stop()
}

// streamTestProvider sends predetermined events through the stream API.
type streamTestProvider struct {
	api    string
	events []provider.AssistantMessageEvent
}

func (p *streamTestProvider) API() provider.Api {
	return provider.Api(p.api)
}

func (p *streamTestProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		for _, event := range p.events {
			result.Push(event)
		}
		endContent := &provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		}
		result.End(endContent)
	}()
	return result, nil
}

func (p *streamTestProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}
