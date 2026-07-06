// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/tui"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
)

// orchStreamEvents is a sequence of orchestrator events that represent one
// agent (coder) streaming thinking → content → tool call → tool result.
func orchStreamEvents() []orchestrator.Event {
	return []orchestrator.Event{
		{Type: orchestrator.EventRunStarted, Payload: map[string]any{"objective": "build it", "topology": "hub"}},
		{Type: orchestrator.EventAgentStarted, AgentID: "c-1", Role: "coder", Model: "gemma",
			Payload: map[string]any{"provider": "google", "thinking": "off"}},
		{Type: orchestrator.EventAgentThinking, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "planning "}},
		{Type: orchestrator.EventAgentThinking, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "the design"}},
		{Type: orchestrator.EventAgentMessage, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "hello "}},
		{Type: orchestrator.EventAgentMessage, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "world"}},
		{Type: orchestrator.EventAgentToolCall, AgentID: "c-1", Role: "coder", Payload: map[string]any{"tool": "bash", "input": `{"command":"ls"}`, "call_id": "t1"}},
		{Type: orchestrator.EventAgentToolResult, AgentID: "c-1", Role: "coder", Payload: map[string]any{"call_id": "t1", "text": "written", "ok": true}},
		{Type: orchestrator.EventAgentFinished, AgentID: "c-1", Role: "coder", Payload: map[string]any{"outcome": "ok"}},
		{Type: orchestrator.EventRunFinished, Payload: map[string]any{"ok": true}},
	}
}

// TestOrchestratorConversation_SingleAgentStreamsThinkingContentTool is a RED
// test for the broken conversation rendering bug. It asserts that the chat
// viewport is not suppressed and that a single agent's thinking, content, and
// tool widgets accumulate in-place rather than being appended as one-line log
// entries per chunk.
func TestOrchestratorConversation_SingleAgentStreamsThinkingContentTool(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()

	film := captureOrchFilmstrip(t, sc, orchStreamEvents())
	frames := film.Frames()
	if len(frames) == 0 {
		t.Fatal("no frames captured")
	}
	last := frames[len(frames)-1]
	node := last.Frame.FindNode("ChatViewport")
	if node == nil {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatal("ChatViewport is suppressed or missing from conversation frame")
	}
	if strings.TrimSpace(node.Text) == "" {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatal("ChatViewport is empty")
	}
	rendered := node.Text

	if strings.Contains(rendered, "[coder] he ") || strings.Contains(rendered, "[coder] llo") || strings.Contains(rendered, "[coder] world") {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatal("rendered text contains per-chunk [coder] lines")
	}
	if strings.Count(rendered, "coder thinking...") < 1 {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatalf("expected at least one 'coder thinking...' header, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "planning the design") {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatalf("expected accumulated thinking text, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "hello world") {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatalf("expected accumulated content text, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "$ ls") {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatalf("expected bash tool widget, got:\n%s", rendered)
	}

	// Ensure the MultiAgentView still exists as a Stats panel. The Conversation
	// + Stats bookends are present; per-agent filter tabs (TabAgent) may also
	// be present — those are the restored per-agent view, not transcript tabs.
	if sc.app.subs.agentView == nil {
		t.Fatal("agentView should still be attached")
	}
	haveBookends := map[orchpanel.AgentTabKind]bool{}
	for _, tab := range sc.app.subs.agentView.Tabs() {
		haveBookends[tab.Kind] = true
	}
	if !haveBookends[orchpanel.TabConversation] || !haveBookends[orchpanel.TabStats] {
		t.Fatalf("missing Conversation/Stats bookends: %+v", sc.app.subs.agentView.Tabs())
	}
}

// TestOrchestratorConversation_TwoAgentsConcurrentThinking asserts that two
// agents streaming thinking concurrently produce two distinct in-place
// updating thinking blocks, not interleaved per-chunk lines.
func TestOrchestratorConversation_TwoAgentsConcurrentThinking(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()

	events := []orchestrator.Event{
		{Type: orchestrator.EventRunStarted, Payload: map[string]any{"objective": "build it", "topology": "hub"}},
		{Type: orchestrator.EventAgentStarted, AgentID: "c-1", Role: "coder", Model: "gemma"},
		{Type: orchestrator.EventAgentStarted, AgentID: "r-1", Role: "reviewer", Model: "qwen"},
		{Type: orchestrator.EventAgentThinking, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "a1"}},
		{Type: orchestrator.EventAgentThinking, AgentID: "r-1", Role: "reviewer", Payload: map[string]any{"text": "b1"}},
		{Type: orchestrator.EventAgentThinking, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "a2"}},
		{Type: orchestrator.EventAgentThinking, AgentID: "r-1", Role: "reviewer", Payload: map[string]any{"text": "b2"}},
		{Type: orchestrator.EventAgentFinished, AgentID: "c-1", Role: "coder"},
		{Type: orchestrator.EventAgentFinished, AgentID: "r-1", Role: "reviewer"},
		{Type: orchestrator.EventRunFinished, Payload: map[string]any{"ok": true}},
	}
	film := captureOrchFilmstrip(t, sc, events)
	last := film.Frames()[len(film.Frames())-1]
	node := last.Frame.FindNode("ChatViewport")
	if node == nil {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatal("ChatViewport missing in final frame")
	}
	rendered := node.Text
	if !strings.Contains(rendered, "a1a2") {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatalf("expected coder thinking accumulated to a1a2, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "b1b2") {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatalf("expected reviewer thinking accumulated to b1b2, got:\n%s", rendered)
	}
	if strings.Count(rendered, "coder thinking...") < 1 || strings.Count(rendered, "reviewer thinking...") < 1 {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatalf("expected distinct thinking headers for both agents, got:\n%s", rendered)
	}
}

// captureOrchFilmstrip records the frame after each translated event is
// applied via the App's command loop, exactly as the production forwarder
// would apply it.
func captureOrchFilmstrip(t *testing.T, sc *orchViewScenario, events []orchestrator.Event) *tui.Filmstrip {
	t.Helper()
	film := tui.NewFilmstrip()
	film.Capture("pre-run", sc.frame(), "")
	for _, ev := range events {
		ne, ok := translateOrchEvent(ev)
		if !ok {
			continue
		}
		sc.engine.ApplySync(func() { sc.app.handleOrchViewEvent(ne) })
		film.Capture(string(ev.Type), sc.frame(), sc.app.subs.statusMsg.Text())
	}
	return film
}

// compile-time check to keep imports relevant if helpers change.
var _ = orchpanel.TabConversation
var _ = orchpanel.TabStats
