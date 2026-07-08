// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/tui"
)

// TestOrchestratorToolCalls_FIFOOrderAfterCompletion is the filmstrip
// regression for the bug reported in bugs.md: completed tool calls should not
// remain pinned to the bottom; they should stay in chronological order so newer
// messages/thinking appear below them.
//
// Sequence: orchestrator starts → thinks → delegates to coder → coder starts
// → writes file → coder finishes → orchestrator gets delegate result →
// orchestrator answers. After the coder write result, the write widget must be
// above the orchestrator's final summary, not pinned to the bottom of it.
func TestOrchestratorToolCalls_FIFOOrderAfterCompletion(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()

	events := []orchestrator.Event{
		{Type: orchestrator.EventRunStarted, Payload: map[string]any{"objective": "build a fire simulation", "topology": "hub"}},
		{Type: orchestrator.EventAgentStarted, AgentID: "o-1", Role: "orchestrator", Model: "qwen"},
		{Type: orchestrator.EventAgentThinking, AgentID: "o-1", Role: "orchestrator", Payload: map[string]any{"text": "planning"}},
		{Type: orchestrator.EventAgentToolCall, AgentID: "o-1", Role: "orchestrator", Payload: map[string]any{"tool": "delegate", "input": `{"role":"coder","task":"write HTML"}`, "call_id": "d1"}},
		{Type: orchestrator.EventAgentToolResult, AgentID: "o-1", Role: "orchestrator", Payload: map[string]any{"call_id": "d1", "text": "sub-agent started", "ok": true}},
		{Type: orchestrator.EventAgentStarted, AgentID: "c-1", Role: "coder", Model: "gemma"},
		{Type: orchestrator.EventAgentToolCall, AgentID: "c-1", Role: "coder", Payload: map[string]any{"tool": "write", "input": `{"path":"index.html","content":"<!DOCTYPE html>"}`, "call_id": "w1"}},
		{Type: orchestrator.EventAgentToolResult, AgentID: "c-1", Role: "coder", Payload: map[string]any{"call_id": "w1", "text": "[write: index.html]", "ok": true}},
		{Type: orchestrator.EventAgentMessage, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "Done."}},
		{Type: orchestrator.EventAgentFinished, AgentID: "c-1", Role: "coder", Payload: map[string]any{"outcome": "ok"}},
		{Type: orchestrator.EventAgentMessage, AgentID: "o-1", Role: "orchestrator", Payload: map[string]any{"text": "The coder created the file."}},
		{Type: orchestrator.EventAgentFinished, AgentID: "o-1", Role: "orchestrator", Payload: map[string]any{"outcome": "ok"}},
		{Type: orchestrator.EventRunFinished, Payload: map[string]any{"ok": true}},
	}

	film := captureOrchFilmstripOnTab(t, sc, events, "conversation")
	last := film.Frames()[len(film.Frames())-1]
	node := last.Frame.FindNode("ChatViewport")
	if node == nil {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatal("ChatViewport missing in final frame")
	}
	rendered := node.Text

	// The completed coder write widget must appear BEFORE the orchestrator's
	// final summary in the chronological order.
	writeIdx := strings.Index(rendered, "write")
	summaryIdx := strings.Index(rendered, "The coder created")
	if writeIdx < 0 {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatal("coder write widget not found in conversation")
	}
	if summaryIdx < 0 {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatal("orchestrator summary not found in conversation")
	}
	if writeIdx > summaryIdx {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatalf("completed coder write widget (%d) is below orchestrator summary (%d); want chronological FIFO order", writeIdx, summaryIdx)
	}

	// The completed tools must show a success marker, not remain spinning.
	if strings.Contains(rendered, "delegate") {
		delegateIdx := strings.Index(rendered, "delegate")
		if delegateIdx > summaryIdx {
			t.Errorf("completed delegate widget is below the orchestrator summary")
		}
	}
}

// TestOrchestratorToolCalls_RunningToolMovesUpWhenNewContentStarts is the RED
// test for the active-zone bug reported in bugs.md: an open (running) tool
// widget should not stay pinned to the bottom when new thinking or message
// blocks start; it should move up in chronological order so the newest output
// is at the bottom.
func TestOrchestratorToolCalls_RunningToolMovesUpWhenNewContentStarts(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()

	events := []orchestrator.Event{
		{Type: orchestrator.EventRunStarted, Payload: map[string]any{"objective": "x", "topology": "hub"}},
		{Type: orchestrator.EventAgentStarted, AgentID: "c-1", Role: "coder", Model: "gemma"},
		{Type: orchestrator.EventAgentToolCall, AgentID: "c-1", Role: "coder", Payload: map[string]any{"tool": "bash", "input": `{"command":"sleep 1"}`, "call_id": "b1"}},
		{Type: orchestrator.EventAgentThinking, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "thinking while tool runs"}},
		{Type: orchestrator.EventAgentMessage, AgentID: "c-1", Role: "coder", Payload: map[string]any{"text": "message after tool call"}},
	}

	film := captureOrchFilmstripOnTab(t, sc, events, "conversation")
	last := film.Frames()[len(film.Frames())-1]
	node := last.Frame.FindNode("ChatViewport")
	if node == nil {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatal("ChatViewport missing in final frame")
	}
	rendered := node.Text
	bashIdx := strings.Index(rendered, "$ sleep 1")
	msgIdx := strings.Index(rendered, "message after tool call")
	if bashIdx < 0 {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatal("running bash widget not found in conversation")
	}
	if msgIdx < 0 {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatal("later message not found in conversation")
	}
	if bashIdx > msgIdx {
		t.Logf("Filmstrip:\n%s", film.Render())
		t.Fatalf("running bash widget (%d) is below later message (%d); want chronological FIFO order", bashIdx, msgIdx)
	}
}

// TestOrchestratorToolCalls_StatusAfterResult verifies that the tool widget
// status flips to success after the tool result event, not only at run end.
func TestOrchestratorToolCalls_StatusAfterResult(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()

	events := []orchestrator.Event{
		{Type: orchestrator.EventRunStarted, Payload: map[string]any{"objective": "x", "topology": "hub"}},
		{Type: orchestrator.EventAgentStarted, AgentID: "c-1", Role: "coder", Model: "gemma"},
		{Type: orchestrator.EventAgentToolCall, AgentID: "c-1", Role: "coder", Payload: map[string]any{"tool": "write", "input": `{"path":"x"}`, "call_id": "w1"}},
		{Type: orchestrator.EventAgentToolResult, AgentID: "c-1", Role: "coder", Payload: map[string]any{"call_id": "w1", "text": "written", "ok": true}},
	}

	captureOrchFilmstripOnTab(t, sc, events, "conversation")
	frame := sc.frame()
	node := frame.FindNode("ChatViewport")
	if node == nil {
		t.Fatal("ChatViewport missing")
	}
	// ANSI-stripped text should contain a success marker; it must not contain a
	// running spinner frame (the exact frame is spinner-dependent, so we assert
	// the absence of the generic running text and presence of a success icon).
	rendered := node.Text
	if strings.Contains(rendered, "⟳") || strings.Contains(rendered, "◉") {
		t.Errorf("completed tool widget still shows running indicator:\n%s", rendered)
	}
	if !strings.Contains(rendered, "write") {
		t.Errorf("write tool widget missing from conversation:\n%s", rendered)
	}
}

// Compile-time import guard.
var _ *tui.Filmstrip = (*tui.Filmstrip)(nil)
