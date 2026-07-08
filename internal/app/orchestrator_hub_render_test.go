// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/orchestrator"
)

// TestOrchestratorHub_RenderAnalyzeDelegateSummary drives a synthetic hub run
// through the chat viewport and asserts the orchestrator's analyze → delegate →
// coder work → summary flow is visible.
func TestOrchestratorHub_RenderAnalyzeDelegateSummary(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()

	events := []orchestrator.Event{
		{Type: orchestrator.EventRunStarted, Payload: map[string]any{"objective": "build a fire simulation", "topology": "hub"}},
		{Type: orchestrator.EventAgentStarted, AgentID: "o-1", Role: "orchestrator", Model: "qwen"},
		{Type: orchestrator.EventAgentThinking, AgentID: "o-1", Role: "orchestrator", Payload: map[string]any{"text": "I need to delegate the coding task."}},
		{Type: orchestrator.EventAgentToolCall, AgentID: "o-1", Role: "orchestrator", Payload: map[string]any{"tool": "delegate", "input": `{"role":"coder","task":"Write a single HTML file that runs a blue/orange/white fire-burning simulation."}`, "call_id": "d1"}},
		{Type: orchestrator.EventAgentToolResult, AgentID: "o-1", Role: "orchestrator", Payload: map[string]any{"call_id": "d1", "text": "HTML file written.", "ok": true}},
		{Type: orchestrator.EventAgentStarted, AgentID: "c-1", Role: "coder", Model: "gemma"},
		{Type: orchestrator.EventAgentThinking, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "Planning the canvas structure."}},
		{Type: orchestrator.EventAgentMessage, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "Done."}},
		{Type: orchestrator.EventAgentFinished, AgentID: "c-1", Role: "coder", Payload: map[string]any{"outcome": "ok"}},
		{Type: orchestrator.EventAgentMessage, AgentID: "o-1", Role: "orchestrator", Payload: map[string]any{"text": "The coder created a single HTML file with the fire simulation."}},
		{Type: orchestrator.EventAgentFinished, AgentID: "o-1", Role: "orchestrator", Payload: map[string]any{"outcome": "ok"}},
		{Type: orchestrator.EventRunFinished, Payload: map[string]any{"ok": true}},
	}

	film := captureOrchFilmstripOnConversationTab(t, sc, events)
	last := film.Frames()[len(film.Frames())-1]
	node := last.Frame.FindNode("ChatViewport")
	if node == nil {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatal("ChatViewport missing in final frame")
	}
	rendered := strings.Join(strings.Fields(node.Text), " ")

	if !strings.Contains(rendered, "delegate") {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatalf("expected delegate tool widget, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "Done.") {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatalf("expected coder content block after delegation, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "The coder created") {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatalf("expected orchestrator summary after coder finished, got:\n%s", rendered)
	}
}
