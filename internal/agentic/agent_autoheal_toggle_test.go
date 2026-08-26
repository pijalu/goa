// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// Regression for the bugs.md entry "/config tool-call fixing is not applied to
// ongoing sessions": toggling execution.auto_heal_tool_calls mid-session must
// take effect on the LIVE agent, not only on sessions created after a restart.
func TestAutoHealToggle_TakesEffectMidSession(t *testing.T) {
	toolCallEvents := []provider.AssistantMessageEvent{
		{Type: provider.EventTextDelta, Delta: "I will call the terminal tool.\n<tool_call>{"},
		{Type: provider.EventTextDelta, Delta: `"name":"terminal","arguments":{"command":"echo hello"}}`},
		{Type: provider.EventTextDelta, Delta: `</tool_call>`},
	}
	p1 := registerTestProvider("autoheal-toggle-off", toolCallEvents)
	mdl := testModel(p1.api)

	calls := 0
	tool := &autoHealMockTool{
		name: "terminal",
		exec: func(input string) (string, error) {
			calls++
			return "hello", nil
		},
	}

	agent := NewAgent(Config{
		Model:        mdl,
		SystemPrompt: "test",
		Tools:        []Tool{tool},
	})
	if agent.AutoHealEnabled() {
		t.Fatal("precondition: auto-heal should default to off")
	}

	// Phase 1: generic XML tool call in text, healing OFF → not executed.
	if _, err := agent.RunAndCollect(context.Background(), "run echo"); err != nil {
		t.Fatalf("first run: %v", err)
	}
	if calls != 0 {
		t.Fatalf("generic XML healed while auto-heal was off (calls=%d)", calls)
	}

	// Phase 2: /config flips execution.auto_heal_tool_calls ON while THIS
	// session stays alive; swap in a fresh provider so the same tool-call
	// stream replays, then the healed call must execute without any restart.
	p2 := registerTestProvider("autoheal-toggle-on", toolCallEvents)
	agent.SetAutoHealToolCalls(true)
	if !agent.AutoHealEnabled() {
		t.Fatal("SetAutoHealToolCalls(true) did not stick")
	}
	agent.SetModel(testModel(p2.api))

	if _, err := agent.RunAndCollect(context.Background(), "run echo again"); err != nil {
		t.Fatalf("second run: %v", err)
	}
	if calls != 1 {
		t.Fatalf("toggle did not take effect on the live session (calls=%d)", calls)
	}

	// Phase 3: flipping OFF again also applies live (no stale snapshot).
	p3 := registerTestProvider("autoheal-toggle-off-again", toolCallEvents)
	agent.SetAutoHealToolCalls(false)
	agent.SetModel(testModel(p3.api))

	if _, err := agent.RunAndCollect(context.Background(), "run echo third"); err != nil {
		t.Fatalf("third run: %v", err)
	}
	if calls != 1 {
		t.Fatalf("toggle-off did not take effect on the live session (calls=%d)", calls)
	}
}
