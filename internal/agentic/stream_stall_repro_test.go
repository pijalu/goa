// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// silentStreamProvider returns a stream that never pushes events and never
// terminates — the "server sent headers then went silent" scenario the
// event-stall watchdog exists for (Issue 21).
type silentStreamProvider struct {
	api   provider.Api
	calls atomic.Int32
}

func (p *silentStreamProvider) API() provider.Api { return p.api }

func (p *silentStreamProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.calls.Add(1)
	// Return an open stream and never push or terminate it: the event-stall
	// watchdog (not the byte-level idle guard) is the only escape.
	return provider.NewAssistantMessageEventStream(64), nil
}

func (p *silentStreamProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

// TestAgent_SilentStreamStall_RecoversOrHangs drives a turn whose stream
// opens but delivers zero events. The stall watchdog must terminate the
// stream and the retry path must engage (or surface a fatal error) — in all
// cases the turn MUST end. A hang here reproduces the 6-hour stuck session.
func TestAgent_SilentStreamStall_RecoversOrHangs(t *testing.T) {
	p := &silentStreamProvider{api: provider.Api(fmt.Sprintf("test-silent-%d", testProviderCounter.Add(1)))}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model: provider.Model{
			ID:         "stall-test",
			Api:        p.API(),
			Provider:   provider.ProviderCustom,
			InputTypes: []string{"text"},
		},
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		StreamOptions: provider.StreamOptions{
			MaxRetries:  2,
			IdleTimeout: 300 * time.Millisecond, // shrink the stall watchdog for the test
		},
	})

	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx, "prompt") }()

	select {
	case err := <-done:
		t.Logf("turn ended (recovered): err=%v, provider calls=%d", err, p.calls.Load())
	case <-time.After(5 * time.Second):
		t.Fatalf("HANG REPRODUCED: silent stream did not recover within 5s (stallTimeout=300ms, MaxRetries=2); provider calls=%d", p.calls.Load())
	}
}

// TestAgent_SilentStreamAfterToolRound_RecoversOrHangs mirrors the real
// session: round 0 streams a tool call (executed fine), round 1's stream is
// silent. The turn must end via the stall watchdog + retry path, not hang.
func TestAgent_SilentStreamAfterToolRound_RecoversOrHangs(t *testing.T) {
	toolEvents := []provider.AssistantMessageEvent{
		{Type: provider.EventToolCallEnd, ToolCall: &provider.ContentBlock{
			Type: provider.ContentBlockToolCall, ToolName: "echo", ToolArguments: `{"text":"hi"}`, ToolCallID: "c1",
		}},
	}
	p := &scriptedStreamProvider{
		api: provider.Api(fmt.Sprintf("test-silent-tool-%d", testProviderCounter.Add(1))),
		steps: []scriptedStreamStep{
			{events: toolEvents},
			// step 2+: silent — use a step with a sentinel error? No: we need
			// an OPEN-but-silent stream, which scriptedStreamProvider cannot
			// express. Handled by wrapping below.
		},
	}
	_ = p
	// Use the silent provider for everything after the first call: compose
	// via a small wrapper that emits tool events once, then goes silent.
	sp := &toolThenSilentProvider{
		api: provider.Api(fmt.Sprintf("test-tts-%d", testProviderCounter.Add(1))),
	}
	provider.RegisterApiProvider(sp)

	echoTool := mockTool{
		name: "echo",
		schema: ToolSchema{
			Name:        "echo",
			Description: "echoes input",
			Schema: map[string]interface{}{
				"type":       "object",
				"properties": map[string]interface{}{"text": map[string]string{"type": "string"}},
			},
		},
	}
	agent := NewAgent(Config{
		Model: provider.Model{
			ID:         "stall-tool-test",
			Api:        sp.API(),
			Provider:   provider.ProviderCustom,
			InputTypes: []string{"text"},
		},
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
		Tools:        []Tool{echoTool},
		StreamOptions: provider.StreamOptions{
			MaxRetries:  2,
			IdleTimeout: 300 * time.Millisecond,
		},
	})

	go func() {
		for range agent.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- agent.Run(ctx, "prompt") }()

	select {
	case err := <-done:
		t.Logf("turn ended (recovered): err=%v, provider calls=%d", err, sp.calls.Load())
	case <-time.After(6 * time.Second):
		t.Fatalf("HANG REPRODUCED after tool round: silent round-1 stream did not recover within 6s; provider calls=%d", sp.calls.Load())
	}
}

// toolThenSilentProvider emits a completed tool call on the first Stream call
// and open-silent streams afterwards.
type toolThenSilentProvider struct {
	api   provider.Api
	calls atomic.Int32
}

func (p *toolThenSilentProvider) API() provider.Api { return p.api }

func (p *toolThenSilentProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	n := p.calls.Add(1)
	if n > 1 {
		return provider.NewAssistantMessageEventStream(64), nil // silent forever
	}
	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		result.Push(provider.AssistantMessageEvent{Type: provider.EventToolCallEnd, ToolCall: &provider.ContentBlock{
			Type: provider.ContentBlockToolCall, ToolName: "echo", ToolArguments: `{"text":"hi"}`, ToolCallID: "c1",
		}})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *toolThenSilentProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}
