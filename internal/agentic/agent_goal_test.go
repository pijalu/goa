// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// capturingProvider is a test provider that records the provider.Context
// passed to Stream for later assertions.
type capturingProvider struct {
	api     provider.Api
	mu      sync.Mutex
	context provider.Context
}

func (p *capturingProvider) API() provider.Api { return p.api }

func (p *capturingProvider) Stream(model provider.Model, ctx provider.Context, opts provider.StreamOptions) (*provider.AssistantMessageEventStream, error) {
	p.mu.Lock()
	p.context = ctx
	p.mu.Unlock()

	result := provider.NewAssistantMessageEventStream(4)
	go func() {
		result.Push(provider.AssistantMessageEvent{Type: provider.EventTextDelta, Delta: "ok"})
		result.End(&provider.AssistantMessage{
			Content:    []provider.ContentBlock{{Type: provider.ContentBlockText, Text: "ok"}},
			StopReason: provider.StopReasonEndTurn,
		})
	}()
	return result, nil
}

func (p *capturingProvider) StreamSimple(model provider.Model, ctx provider.Context, opts provider.SimpleStreamOptions) (*provider.AssistantMessageEventStream, error) {
	base := provider.BuildSimpleOptions(model, opts)
	return p.Stream(model, ctx, base)
}

func (p *capturingProvider) Captured() provider.Context {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.context
}

func registerCapturingProvider(name string) *capturingProvider {
	uniqueID := testProviderCounter.Add(1)
	p := &capturingProvider{api: provider.Api(fmt.Sprintf("test-capture-%s-%d", name, uniqueID))}
	provider.RegisterApiProvider(p)
	return p
}

// mockGoalProvider returns a stable static reminder and dynamic progress that
// can be distinguished in assertions.
type mockGoalProvider struct {
	staticReminder string
	dynamicProgress string
}

func (m *mockGoalProvider) ActiveGoalReminder() string { return m.staticReminder }
func (m *mockGoalProvider) ActiveGoalProgress() string { return m.dynamicProgress }

func TestBuildProviderContext_GoalProgressSeparateMessage(t *testing.T) {
	cap := registerCapturingProvider("goal-progress")

	agent := NewAgent(Config{
		Model:        testModel(cap.api),
		SystemPrompt: "system prompt",
		Logger:       NewLogger(Error),
		GoalStateProvider: &mockGoalProvider{
			staticReminder:  "STATIC GOAL REMINDER",
			dynamicProgress: "DYNAMIC PROGRESS LINE",
		},
	})

	ctx := context.Background()
	if err := agent.Run(ctx, "user turn"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	pctx := cap.Captured()
	if !strings.Contains(pctx.SystemPrompt, "STATIC GOAL REMINDER") {
		t.Errorf("system prompt should contain static reminder, got %q", pctx.SystemPrompt)
	}
	if strings.Contains(pctx.SystemPrompt, "DYNAMIC PROGRESS LINE") {
		t.Errorf("system prompt should NOT contain dynamic progress, got %q", pctx.SystemPrompt)
	}

	// The dynamic progress must be injected as a separate system message placed
	// immediately before the last user message, NOT baked into the user
	// message's content. Prepending to the user message made its bytes
	// turn-specific, busting the cached prefix on the next turn.
	progressIdx := indexOfSystemContaining(pctx.Messages, "DYNAMIC PROGRESS LINE")
	lastUserIdx := indexOfLastRole(pctx.Messages, provider.RoleUser)
	if progressIdx < 0 {
		t.Fatalf("dynamic progress should be injected as a system message; messages: %+v", pctx.Messages)
	}
	if lastUserIdx < 0 {
		t.Fatal("expected at least one user message")
	}
	if progressIdx != lastUserIdx-1 {
		t.Errorf("progress system message should immediately precede the last user message: progress@%d lastUser@%d",
			progressIdx, lastUserIdx)
	}
	// The last user message must carry only the verbatim user text.
	lastUser := pctx.Messages[lastUserIdx]
	for _, b := range lastUser.Content {
		if strings.Contains(b.Text, "DYNAMIC PROGRESS LINE") {
			t.Errorf("last user message must not contain progress (cache-stable verbatim text): %q", b.Text)
		}
	}
}

func TestBuildProviderContext_NoGoalProvider_NoInjection(t *testing.T) {
	cap := registerCapturingProvider("no-goal")

	agent := NewAgent(Config{
		Model:        testModel(cap.api),
		SystemPrompt: "system prompt",
		Logger:       NewLogger(Error),
	})

	ctx := context.Background()
	if err := agent.Run(ctx, "user turn"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	pctx := cap.Captured()
	if pctx.SystemPrompt != "system prompt" {
		t.Errorf("system prompt should be unchanged, got %q", pctx.SystemPrompt)
	}
}

// indexOfSystemContaining returns the index of the first system message whose
// content contains needle, or -1.
func indexOfSystemContaining(msgs []provider.Message, needle string) int {
	for i, m := range msgs {
		if m.Role != provider.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			if strings.Contains(b.Text, needle) {
				return i
			}
		}
	}
	return -1
}

// indexOfLastRole returns the index of the last message with the given role,
// or -1.
func indexOfLastRole(msgs []provider.Message, role provider.Role) int {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == role {
			return i
		}
	}
	return -1
}
