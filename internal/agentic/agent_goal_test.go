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
	// Neither the static reminder nor the dynamic progress may live in the
	// system prompt: the prompt is the provider-cached prefix, and any goal
	// text there busts the whole cache on goal create/destroy/status-flip
	// (bugs.md CRITICAL: /goal destroy caching).
	if strings.Contains(pctx.SystemPrompt, "STATIC GOAL REMINDER") {
		t.Errorf("system prompt must NOT contain static reminder (cached prefix): %q", pctx.SystemPrompt)
	}
	if strings.Contains(pctx.SystemPrompt, "DYNAMIC PROGRESS LINE") {
		t.Errorf("system prompt should NOT contain dynamic progress, got %q", pctx.SystemPrompt)
	}

	// Both the static reminder and the dynamic progress must be injected as
	// volatile system slot messages immediately before the last user message
	// (static first, then dynamic), NOT baked into the cached prefix or the
	// user message content.
	staticIdx := indexOfSystemContaining(pctx.Messages, "STATIC GOAL REMINDER")
	progressIdx := indexOfSystemContaining(pctx.Messages, "DYNAMIC PROGRESS LINE")
	lastUserIdx := indexOfLastRole(pctx.Messages, provider.RoleUser)
	if staticIdx < 0 {
		t.Fatalf("static reminder should be injected as a system slot message; messages: %+v", pctx.Messages)
	}
	if progressIdx < 0 {
		t.Fatalf("dynamic progress should be injected as a system message; messages: %+v", pctx.Messages)
	}
	if lastUserIdx < 0 {
		t.Fatal("expected at least one user message")
	}
	if staticIdx != lastUserIdx-2 || progressIdx != lastUserIdx-1 {
		t.Errorf("goal slots should immediately precede the last user message (static, then dynamic): static@%d progress@%d lastUser@%d",
			staticIdx, progressIdx, lastUserIdx)
	}
	// The last user message must carry only the verbatim user text.
	lastUser := pctx.Messages[lastUserIdx]
	for _, b := range lastUser.Content {
		if strings.Contains(b.Text, "DYNAMIC PROGRESS LINE") || strings.Contains(b.Text, "STATIC GOAL REMINDER") {
			t.Errorf("last user message must not contain goal text (cache-stable verbatim text): %q", b.Text)
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

// TestBuildProviderContext_GoalDestroyKeepsCachedPrefixStable is the core
// regression for bugs.md "CRITICAL: /goal destroy caching": destroying a goal
// (static reminder disappears) must NOT change the system prompt or the
// cached history prefix — goal text lives only in the volatile pre-user slot,
// so the provider prompt cache survives a destroy.
func TestBuildProviderContext_GoalDestroyKeepsCachedPrefixStable(t *testing.T) {
	cap := registerCapturingProvider("goal-destroy")
	gp := &mockGoalProvider{
		staticReminder:  "STATIC GOAL REMINDER",
		dynamicProgress: "DYNAMIC PROGRESS LINE",
	}
	agent := NewAgent(Config{
		Model:             testModel(cap.api),
		SystemPrompt:      "system prompt",
		Logger:            NewLogger(Error),
		GoalStateProvider: gp,
	})

	ctx := context.Background()
	if err := agent.Run(ctx, "turn one"); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	first := cap.Captured()
	if first.SystemPrompt != "system prompt" {
		t.Fatalf("turn 1: system prompt must carry no goal text, got %q", first.SystemPrompt)
	}

	// Destroy the goal: the provider now reports no goal content.
	gp.staticReminder = ""
	gp.dynamicProgress = ""

	if err := agent.Run(ctx, "turn two"); err != nil {
		t.Fatalf("Run 2: %v", err)
	}
	second := cap.Captured()

	// The cached prefix must be byte-identical across the destroy: same system
	// prompt, and the first turn's HISTORY (its messages minus the volatile
	// goal slots, which are per-request only and never enter a.history) must
	// appear unchanged as the leading part of the second request.
	if second.SystemPrompt != first.SystemPrompt {
		t.Errorf("destroy changed the system prompt (cache bust):\n  before=%q\n  after=%q",
			first.SystemPrompt, second.SystemPrompt)
	}
	firstHistory := stripGoalSlots(first.Messages)
	for i, m := range firstHistory {
		if i >= len(second.Messages) {
			t.Fatalf("second request shrank below the first-turn history (%d < %d)", len(second.Messages), len(firstHistory))
		}
		if !messagesEqual(second.Messages[i], m) {
			t.Errorf("history message %d changed across destroy (cache bust):\n  before=%+v\n  after=%+v", i, m, second.Messages[i])
		}
	}
	assertNoGoalText(t, second.Messages)
}

// assertNoGoalText fails the test if any message carries goal slot content.
func assertNoGoalText(t *testing.T, msgs []provider.Message) {
	t.Helper()
	for i, m := range msgs {
		for _, b := range m.Content {
			if strings.Contains(b.Text, "STATIC GOAL REMINDER") || strings.Contains(b.Text, "DYNAMIC PROGRESS LINE") {
				t.Errorf("goal text leaked into message %d after destroy: %q", i, b.Text)
			}
		}
	}
}

// stripGoalSlots removes the volatile goal slot messages (system role with a
// "[goal]"/"[goal progress]" marker) from a message slice, leaving the
// provider-cached history.
func stripGoalSlots(msgs []provider.Message) []provider.Message {
	out := make([]provider.Message, 0, len(msgs))
	for _, m := range msgs {
		if m.Role == provider.RoleSystem && len(m.Content) > 0 &&
			(strings.HasPrefix(m.Content[0].Text, "[goal]") || strings.HasPrefix(m.Content[0].Text, "[goal progress]")) {
			continue
		}
		out = append(out, m)
	}
	return out
}

// messagesEqual compares two provider messages by role and text content.
func messagesEqual(a, b provider.Message) bool {
	if a.Role != b.Role || len(a.Content) != len(b.Content) {
		return false
	}
	for i := range a.Content {
		if a.Content[i].Text != b.Content[i].Text || a.Content[i].Type != b.Content[i].Type {
			return false
		}
	}
	return true
}
