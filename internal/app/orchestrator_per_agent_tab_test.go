// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/orchestrator"
)

// TestOrchestratorPerAgentTab_ToolWidgetVisible is the RED regression for the
// bug reported in bugs.md: when switching to a dedicated agent tab (e.g. coder),
// the agent's tool calls should be visible, not just text/thinking blocks.
func TestOrchestratorPerAgentTab_ToolWidgetVisible(t *testing.T) {
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

	// Switch to the dedicated coder tab.
	if !sc.app.selectAgentTab("c-1") {
		t.Fatal("selectAgentTab(c-1) failed")
	}
	frame := sc.frame()
	node := frame.FindNode("ChatViewport")
	if node == nil {
		t.Fatal("ChatViewport missing on coder tab")
	}
	if !strings.Contains(node.Text, "write") {
		t.Logf("coder tab visible text:\n%s", node.Text)
		t.Errorf("coder tab should show the write tool widget")
	}
	if !strings.Contains(node.Text, "Done.") {
		t.Logf("coder tab visible text:\n%s", node.Text)
		t.Errorf("coder tab should show the coder message")
	}
}
