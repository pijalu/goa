// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/agentic/provider/schema"
)

// steeringTestSource is a SteeringSource the test can append to mid-turn.
type steeringTestSource struct {
	mu      sync.Mutex
	pending []string
}

func (s *steeringTestSource) Add(text string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.pending = append(s.pending, text)
}

func (s *steeringTestSource) Drain() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := s.pending
	s.pending = nil
	return p
}

// steeringCaptureProvider emits one tool call on its first stream, then a
// plain-text answer on later streams. It records the messages of every
// request so the test can assert the steering was woven into the round-2
// request and that round-2 is a prefix-extension of round-1.
type steeringCaptureProvider struct {
	api      provider.Api
	toolName string

	mu       sync.Mutex
	attempts int
	requests [][]schema.Message
	// onFirstStreamStart lets the test enqueue steering after round 1 begins.
	onAfterFirst func()
}

func (p *steeringCaptureProvider) API() provider.Api { return p.api }

func (p *steeringCaptureProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.attempts++
	attempt := p.attempts
	// Snapshot the request messages (deep enough for prefix/text comparison).
	req := make([]schema.Message, len(ctx.Messages))
	copy(req, ctx.Messages)
	p.requests = append(p.requests, req)
	p.mu.Unlock()

	result := provider.NewAssistantMessageEventStream(64)
	go func() {
		if attempt == 1 {
			result.Push(provider.AssistantMessageEvent{
				Type:         provider.EventToolCallEnd,
				ContentIndex: 0,
				ToolCall: &provider.ContentBlock{
					Type:          provider.ContentBlockToolCall,
					ToolCallID:    "call_1",
					ToolName:      p.toolName,
					ToolArguments: `{"arg":"x"}`,
				},
			})
			if p.onAfterFirst != nil {
				p.onAfterFirst()
			}
		} else {
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextStart, ContentIndex: 0})
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "done."})
			result.Push(provider.AssistantMessageEvent{Type: provider.EventTextEnd, ContentIndex: 0})
		}
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "done"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

// StreamSimple satisfies provider.ApiProvider.
func (p *steeringCaptureProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	return p.Stream(model, ctx, provider.StreamOptions{})
}

// TestAgent_SteeringWovenMidTurn is the regression test for the steering-
// lateness bug: steering typed while the agent is running must be woven into
// the CURRENT turn (as a user message at the tail) so the NEXT provider
// request already contains it — not delivered as a late, separate turn.
// Cache-safety (guideline #9) is asserted by checking the round-2 request is a
// strict prefix-extension of round-1 plus the tool result + steering.
func TestAgent_SteeringWovenMidTurn(t *testing.T) {
	src := &steeringTestSource{}
	p := &steeringCaptureProvider{
		api:      provider.Api("test-steering-midturn"),
		toolName: "mock_tool",
		onAfterFirst: func() {
			// Steering typed while round 1's tool executes.
			src.Add("actually, use the other approach")
		},
	}
	provider.RegisterApiProvider(p)

	agent := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "You are a test assistant.",
		Logger:       NewLogger(Error),
		Tools: []Tool{mockTool{
			name:   "mock_tool",
			schema: ToolSchema{Name: "mock_tool", Description: "test"},
		}},
	})
	agent.SetSteeringSource(src)

	go func() { for range agent.Output { } }()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := agent.Run(ctx, "do the work"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	if len(p.requests) < 2 {
		t.Fatalf("expected >=2 provider requests (tool round + answer), got %d", len(p.requests))
	}
	assertSteeringIsLastUserMsg(t, p.requests[1], "other approach")
	assertPrefixExtension(t, p.requests[0], p.requests[1])
}

// assertSteeringIsLastUserMsg verifies the last user message in the request
// contains want (the steering was woven in before this round's stream).
func assertSteeringIsLastUserMsg(t *testing.T, msgs []schema.Message, want string) {
	t.Helper()
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role != schema.RoleUser {
			continue
		}
		for _, c := range msgs[i].Content {
			if c.Type == schema.ContentBlockText && strings.Contains(c.Text, want) {
				return
			}
		}
		t.Errorf("last user message does not contain steering %q: %+v", want, msgs[i].Content)
		return
	}
	t.Error("no user message found in request")
}

// assertPrefixExtension verifies later is a strict prefix-extension of earlier:
// every message of earlier appears unchanged at the same index in later
// (guideline #9 — append-only, never a history rewrite).
func assertPrefixExtension(t *testing.T, earlier, later []schema.Message) {
	t.Helper()
	if len(later) < len(earlier) {
		t.Fatalf("later request (%d msgs) shorter than earlier (%d msgs) — history rewritten, not appended", len(later), len(earlier))
	}
	for i := range earlier {
		if !messagesEqual(earlier[i], later[i]) {
			t.Errorf("message %d differs between rounds — prefix violated (guideline #9)", i)
			return
		}
	}
}
