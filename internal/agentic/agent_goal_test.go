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

func TestBuildProviderContext_GoalProgressInUserMessage(t *testing.T) {
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

	var userMsgs []provider.Message
	for _, m := range pctx.Messages {
		if m.Role == provider.RoleUser {
			userMsgs = append(userMsgs, m)
		}
	}
	if len(userMsgs) == 0 {
		t.Fatal("expected at least one user message")
	}
	lastUser := userMsgs[len(userMsgs)-1]
	found := false
	for _, block := range lastUser.Content {
		if block.Type == provider.ContentBlockText && strings.Contains(block.Text, "DYNAMIC PROGRESS LINE") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("dynamic progress should be in the last user message, got %+v", lastUser.Content)
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
