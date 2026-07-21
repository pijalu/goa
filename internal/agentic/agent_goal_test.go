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

	// kimi-code parity (bugs.md 2026-07-21, design A): the goal context is
	// PERSISTED into the conversation once per turn as ordinary USER-role
	// messages appended right after the turn's user message — static first,
	// then dynamic. It is never merged per-request and never system role
	// (strict chat templates reject mid-conversation system messages with
	// HTTP 400 "System message must be at the beginning").
	staticIdx := indexOfSlotContaining(pctx.Messages, "STATIC GOAL REMINDER")
	progressIdx := indexOfSlotContaining(pctx.Messages, "DYNAMIC PROGRESS LINE")
	turnIdx := indexOfSlotContaining(pctx.Messages, "user turn")
	if staticIdx < 0 {
		t.Fatalf("static reminder should be persisted as a user-role history message; messages: %+v", pctx.Messages)
	}
	if progressIdx < 0 {
		t.Fatalf("dynamic progress should be persisted as a user-role history message; messages: %+v", pctx.Messages)
	}
	if turnIdx < 0 {
		t.Fatal("expected the turn's user message")
	}
	if staticIdx != turnIdx+1 || progressIdx != turnIdx+2 {
		t.Errorf("goal reminders must immediately follow the turn's user message (static, then dynamic): turn@%d static@%d progress@%d",
			turnIdx, staticIdx, progressIdx)
	}
	// No system-role message may carry goal text — the only system content
	// allowed is the leading system prompt itself (LM Studio 400 contract).
	for i, m := range pctx.Messages {
		if m.Role != provider.RoleSystem {
			continue
		}
		for _, b := range m.Content {
			if strings.Contains(b.Text, "STATIC GOAL REMINDER") || strings.Contains(b.Text, "DYNAMIC PROGRESS LINE") {
				t.Errorf("goal text in system-role message %d breaks strict chat templates (400): %q", i, b.Text)
			}
		}
	}
	// The turn's user message must carry only the verbatim user text.
	for _, b := range pctx.Messages[turnIdx].Content {
		if strings.Contains(b.Text, "DYNAMIC PROGRESS LINE") || strings.Contains(b.Text, "STATIC GOAL REMINDER") {
			t.Errorf("turn user message must not contain goal text (verbatim text): %q", b.Text)
		}
	}
}

// TestGoalReminder_InjectedOncePerTurn pins the per-turn cadence: every Run
// appends ONE fresh reminder pair (with the current progress snapshot), so
// turn N+1's history carries both snapshots as ordinary append-only history.
func TestGoalReminder_InjectedOncePerTurn(t *testing.T) {
	cap := registerCapturingProvider("goal-per-turn")
	gp := &mockGoalProvider{
		staticReminder:  "STATIC GOAL REMINDER",
		dynamicProgress: "PROGRESS TURN 1",
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
	gp.dynamicProgress = "PROGRESS TURN 2"
	if err := agent.Run(ctx, "turn two"); err != nil {
		t.Fatalf("Run 2: %v", err)
	}

	pctx := cap.Captured()
	if indexOfSlotContaining(pctx.Messages, "PROGRESS TURN 1") < 0 {
		t.Errorf("turn 1's reminder snapshot must stay in history (append-only)")
	}
	second := indexOfSlotContaining(pctx.Messages, "PROGRESS TURN 2")
	if second < 0 {
		t.Fatalf("turn 2 must append a fresh reminder snapshot")
	}
	if n := countSlotContaining(pctx.Messages, "STATIC GOAL REMINDER"); n != 2 {
		t.Errorf("expected exactly 2 persisted static reminders (one per turn), got %d", n)
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

// indexOfSlotContaining returns the index of the first user-role slot message
// whose content contains needle, or -1. Goal slots are user-role by contract
// (strict chat templates reject mid-conversation system messages).
func indexOfSlotContaining(msgs []provider.Message, needle string) int {
	for i, m := range msgs {
		if m.Role != provider.RoleUser {
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

// TestBuildProviderContext_GoalDestroyKeepsCachedPrefixStable is the core
// regression for the goal-destroy cache contract under the persist-per-turn
// design (bugs.md 2026-07-21, design A): destroying a goal must not rewrite
// any existing byte — turn 1's persisted reminder stays in history
// (append-only, kimi-code parity) — and turn 2 must simply append NO new
// goal text. The whole prefix stays cache-servable.
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

	// The cached prefix must be byte-identical across the destroy: same
	// system prompt, and the first request's ENTIRE message list (reminders
	// included — they are ordinary persisted history now) must appear
	// unchanged as the leading part of the second request.
	if second.SystemPrompt != first.SystemPrompt {
		t.Errorf("destroy changed the system prompt (cache bust):\n  before=%q\n  after=%q",
			first.SystemPrompt, second.SystemPrompt)
	}
	for i, m := range first.Messages {
		if i >= len(second.Messages) {
			t.Fatalf("second request shrank below the first (%d < %d)", len(second.Messages), len(first.Messages))
		}
		if !messagesEqual(second.Messages[i], m) {
			t.Errorf("history message %d changed across destroy (append-only violated):\n  before=%+v\n  after=%+v", i, m, second.Messages[i])
		}
	}
	// Turn 2 must append NO new goal text: exactly the turn-1 pair exists.
	if n := countSlotContaining(second.Messages, "STATIC GOAL REMINDER"); n != 1 {
		t.Errorf("destroyed goal must not append new reminders: got %d static reminders, want the 1 from turn 1", n)
	}
	if n := countSlotContaining(second.Messages, "DYNAMIC PROGRESS LINE"); n != 1 {
		t.Errorf("destroyed goal must not append new progress: got %d progress messages, want the 1 from turn 1", n)
	}
}

// countSlotContaining returns how many user-role messages contain needle.
func countSlotContaining(msgs []provider.Message, needle string) int {
	n := 0
	for _, m := range msgs {
		if m.Role != provider.RoleUser {
			continue
		}
		for _, b := range m.Content {
			if strings.Contains(b.Text, needle) {
				n++
			}
		}
	}
	return n
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
