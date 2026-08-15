// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// countingPreTurnProvider returns a fixed set of messages once, then nothing
// (mirroring the schedule store's claim-once contract). It records how many
// times PreTurnMessages was called.
type countingPreTurnProvider struct {
	mu        sync.Mutex
	calls     int
	delivered bool
	msgs      []string
}

func (p *countingPreTurnProvider) PreTurnMessages() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.delivered {
		return nil
	}
	p.delivered = true
	return p.msgs
}

func (p *countingPreTurnProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestPreTurnProvider_DeliversUserMessage verifies that PreTurnProvider content
// is appended as user-role messages ahead of the user's actual input on the
// next turn (P18 delivery contract: due jobs inject as user messages).
func TestPreTurnProvider_DeliversUserMessage(t *testing.T) {
	events := []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "Understood."},
	}
	p := registerTestProvider("preturn", events)
	mdl := testModel(p.api)

	provider_ := &countingPreTurnProvider{
		msgs: []string{"[SCHEDULE REMINDER]\nreminders_json: [{\"reminder_prompt\":\"ship it\"}]"},
	}

	agent := NewAgent(Config{
		Model:           mdl,
		SystemPrompt:    "test",
		PreTurnProvider: provider_,
	})

	if _, err := agent.RunAndCollect(context.Background(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// The pre-turn message must precede the user input in the history and be
	// user-role.
	var preturnIdx, userIdx = -1, -1
	for i, msg := range agent.history {
		if strings.Contains(msg.Content, "SCHEDULE REMINDER") {
			preturnIdx = i
		}
		if msg.Content == "hello" && msg.Role == User {
			userIdx = i
		}
	}
	if preturnIdx == -1 {
		t.Fatal("pre-turn message was not appended to history")
	}
	if userIdx == -1 {
		t.Fatal("user input was not appended to history")
	}
	if preturnIdx >= userIdx {
		t.Fatalf("pre-turn message (idx %d) must precede user input (idx %d)", preturnIdx, userIdx)
	}
	if got := agent.history[preturnIdx].Role; got != User {
		t.Fatalf("pre-turn message role = %v, want User", got)
	}
}

// TestPreTurnProvider_EmptyReturnsNothing verifies the provider is invoked but
// an empty result adds no messages.
func TestPreTurnProvider_EmptyReturnsNothing(t *testing.T) {
	events := []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "OK."},
	}
	p := registerTestProvider("preturn-empty", events)
	mdl := testModel(p.api)

	provider_ := &countingPreTurnProvider{msgs: nil}

	agent := NewAgent(Config{
		Model:           mdl,
		SystemPrompt:    "test",
		PreTurnProvider: provider_,
	})
	if _, err := agent.RunAndCollect(context.Background(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if provider_.callCount() == 0 {
		t.Fatal("PreTurnMessages should still be invoked once per turn")
	}
	for _, msg := range agent.history {
		if strings.Contains(msg.Content, "SCHEDULE REMINDER") {
			t.Fatal("no pre-turn message should be appended when provider returns empty")
		}
	}
}
