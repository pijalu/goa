// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/orchestrator"
)

// TestOrchestratorConversation_ToolWidgetVisible is the RED regression for the
// bug reported in bugs.md: tool calls from agents must be visible in the unified
// conversation view. Per-agent tabs were removed, so the conversation view is
// the only place where agent activity is rendered.
func TestOrchestratorConversation_ToolWidgetVisible(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()

	events := []orchestrator.Event{
		{Type: orchestrator.EventRunStarted, Payload: map[string]any{"objective": "x", "topology": "hub"}},
		{Type: orchestrator.EventAgentStarted, AgentID: "c-1", Role: "coder", Model: "gemma"},
		{Type: orchestrator.EventAgentToolCall, AgentID: "c-1", Role: "coder", Payload: map[string]any{"tool": "write", "input": `{"path":"x"}`, "call_id": "w1"}},
		{Type: orchestrator.EventAgentToolResult, AgentID: "c-1", Role: "coder", Payload: map[string]any{"call_id": "w1", "text": "written", "ok": true}},
		{Type: orchestrator.EventAgentMessage, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "Done."}},
	}

	captureOrchFilmstripOnTab(t, sc, events, "conversation")

	frame := sc.frame()
	node := frame.FindNode("ChatViewport")
	if node == nil {
		t.Fatal("ChatViewport missing on conversation view")
	}
	if !strings.Contains(node.Text, "write") {
		t.Logf("conversation visible text:\n%s", node.Text)
		t.Errorf("conversation view should show the write tool widget")
	}
	if !strings.Contains(node.Text, "Done.") {
		t.Logf("conversation visible text:\n%s", node.Text)
		t.Errorf("conversation view should show the coder message")
	}
}
