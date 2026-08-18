// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// keyCapturingProvider records the PromptCacheKey each stream was opened
// with, so tests can assert which cache identity actually reached the wire.
type keyCapturingProvider struct {
	api   provider.Api
	mu    sync.Mutex
	keys  []string
	model provider.Model
}

func (p *keyCapturingProvider) API() provider.Api { return p.api }

func (p *keyCapturingProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.keys = append(p.keys, opts.PromptCacheKey)
	p.model = model
	p.mu.Unlock()
	stream := provider.NewAssistantMessageEventStream(4)
	go func() {
		stream.End(&provider.AssistantMessage{})
	}()
	return stream, nil
}

func (p *keyCapturingProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, provider.Context{}, provider.StreamOptions{PromptCacheKey: opts.SessionID})
}

func (p *keyCapturingProvider) recorded() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.keys...)
}

// TestAgentStreamStampsLiveCacheIdentity pins the Hard Rule 7 invariant the
// Agent.stream wrapper provides: the key on the wire is derived from agent
// state at OPEN time, so a generation rotation between two stream opens is
// reflected immediately instead of silently reusing a stale identity (the
// diverged-prefix cache eviction the rule exists to prevent).
func TestAgentStreamStampsLiveCacheIdentity(t *testing.T) {
	p := &keyCapturingProvider{api: provider.Api(fmt.Sprintf("test-key-stamp-%d", time.Now().UnixNano()))}
	provider.RegisterApiProvider(p)
	a := NewAgent(Config{
		Model:        provider.Model{ID: "m", Api: p.API(), Provider: provider.ProviderCustom},
		SystemPrompt: "sys",
		Logger:       NewLogger(Error),
	})

	stream, err := a.stream(a.cfg.Model, provider.Context{}, provider.StreamOptions{})
	if err != nil {
		t.Fatalf("first stream: %v", err)
	}
	_ = stream.Result()

	a.SetHistory([]Message{{Role: User, Content: "replacement rotates the generation"}})

	stream2, err := a.stream(a.cfg.Model, provider.Context{}, provider.StreamOptions{})
	if err != nil {
		t.Fatalf("second stream: %v", err)
	}
	_ = stream2.Result()

	keys := p.recorded()
	if len(keys) != 2 {
		t.Fatalf("expected 2 recorded stream opens, got %d", len(keys))
	}
	if keys[0] == "" {
		t.Fatal("first stream carried no PromptCacheKey")
	}
	if keys[1] == keys[0] {
		t.Fatal("stream opened after a history replacement reused the stale cache key")
	}
	// The active key must track the LAST opened stream so notice draining is
	// attributed to the sequence that actually just ran.
	a.mu.Lock()
	active := a.activeCacheKey
	a.mu.Unlock()
	if active != keys[1] {
		t.Fatalf("activeCacheKey = %q, want the key of the last opened stream %q", active, keys[1])
	}
}

// TestAgentStreamRoundCapIsExplicit verifies MaxStreamRounds is a REAL cap:
// with a provider that never converges (tool call on every stream) and the
// silent-round guardrail disabled, the turn must still end after exactly
// MaxStreamRounds rounds plus the bounded recovery rounds — not loop forever.
func TestAgentStreamRoundCapIsExplicit(t *testing.T) {
	toolCallEvents := []provider.AssistantMessageEvent{
		{Type: provider.EventToolCallEnd, ToolCall: &provider.ContentBlock{
			Type: provider.ContentBlockToolCall, ToolName: "echo", ToolArguments: `{"text":"hi"}`,
		}},
	}
	steps := make([]scriptedStreamStep, 0, 8)
	for i := 0; i < 8; i++ {
		steps = append(steps, scriptedStreamStep{events: toolCallEvents})
	}
	p := &scriptedStreamProvider{
		api:   provider.Api(fmt.Sprintf("test-round-cap-%d", time.Now().UnixNano())),
		steps: steps,
	}
	provider.RegisterApiProvider(p)

	echoTool := mockTool{name: "echo", schema: ToolSchema{
		Name: "echo", Description: "echo",
		Schema: map[string]any{"type": "object", "properties": map[string]any{}},
	}}
	a := NewAgent(Config{
		Model:        provider.Model{ID: "round-cap", Api: p.API(), Provider: provider.ProviderCustom, InputTypes: []string{"text"}},
		SystemPrompt: "sys",
		Logger:       NewLogger(Error),
		Tools:        []Tool{echoTool},
		// Guardrail disabled and an explicit round cap: only the cap can end
		// the turn (2 rounds + 3 recovery rounds = 5 stream opens).
		MaxConsecutiveToolRounds: 0,
		MaxStreamRounds:          2,
	})
	go func() {
		for range a.Output {
		}
	}()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- a.Run(ctx, "go") }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Run: %v", err)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("turn did not end: MaxStreamRounds cap is not enforced")
	}
	if got := p.Calls(); got != 5 {
		t.Fatalf("expected 5 stream calls (2 capped rounds + 3 recovery rounds), got %d", got)
	}
}
