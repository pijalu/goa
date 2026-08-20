// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/tui"
)

// waitingToolWidgets returns every tool widget currently in the chat viewport.
func waitingToolWidgets(chat *tui.ChatViewport) []*tui.ToolExecutionComponent {
	var out []*tui.ToolExecutionComponent
	for _, c := range chat.Children() {
		if tc, ok := c.(*tui.ToolExecutionComponent); ok {
			out = append(out, tc)
		}
	}
	return out
}

// TestUI_NamelessToolCallDeltaNoBlankWidget reproduces "Empty tool
// TUI" end-to-end: an OpenAI-style stream ships the call id first and the
// tool name in a later chunk. The app must create exactly ONE widget (at the
// first named delta) — never a blank-header box.
func TestUI_NamelessToolCallDeltaNoBlankWidget(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	// Chunk 1: id only, NO name — historically created the blank widget.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolCallID: "c1", ToolName: "", ToolInput: "", IsDelta: true})
	if ws := waitingToolWidgets(sc.chat); len(ws) != 0 {
		t.Fatalf("nameless delta must create no widget, got %d", len(ws))
	}
	// Chunk 2: name + args prefix.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolCallID: "c1", ToolName: "bash", ToolInput: `{"command":"ls`, IsDelta: true})
	// Chunk 3: more args.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolCallID: "c1", ToolName: "bash", ToolInput: `{"command":"ls -la"}`, IsDelta: true})
	// Final + start + result.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolCallID: "c1", ToolName: "bash", ToolInput: `{"command":"ls -la"}`})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolStart, State: agentic.StateToolCall,
		ToolCallID: "c1", ToolName: "bash"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolCallID: "c1", ToolName: "bash", Text: "file1\nfile2"})

	ws := waitingToolWidgets(sc.chat)
	if len(ws) != 1 {
		t.Fatalf("exactly one widget expected, got %d (blank orphan!)", len(ws))
	}
	if ws[0].ToolName() != "bash" {
		t.Errorf("widget name = %q, want bash", ws[0].ToolName())
	}
	if ws[0].Status() != tui.ToolSuccess {
		t.Errorf("widget must be Success after result, got %v", ws[0].Status())
	}
}

// TestUI_QueuedToolsShowWaiting reproduces Bug W end-to-end: in a
// multi-call batch, calls the scheduler has NOT started yet stay Pending and
// render ⧖ "waiting Ns…" — only the executing call shows "elapsed".
func TestUI_QueuedToolsShowWaiting(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	// Three finalized bash calls arrive in one burst (args complete).
	for _, call := range []struct{ id, cmd string }{
		{"c1", "go test ./..."}, {"c2", "ls -la"}, {"c3", "go vet"},
	} {
		sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
			ToolCallID: call.id, ToolName: "bash", ToolInput: `{"command":"` + call.cmd + `"}`})
	}

	// Only c1 has started executing so far.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolStart, State: agentic.StateToolCall,
		ToolCallID: "c1", ToolName: "bash"})

	ws := waitingToolWidgets(sc.chat)
	if len(ws) != 3 {
		t.Fatalf("expected 3 widgets, got %d", len(ws))
	}
	if ws[0].Status() != tui.ToolRunning {
		t.Errorf("c1 must be Running after EventToolStart, got %v", ws[0].Status())
	}
	for i, w := range ws[1:] {
		if w.Status() != tui.ToolPending {
			t.Errorf("queued widget %d must stay Pending, got %v", i+2, w.Status())
		}
		rendered := ansi.Strip(strings.Join(w.Render(100), "\n"))
		if !strings.Contains(rendered, "⧖") {
			t.Errorf("queued widget %d must show ⧖ hourglass, got:\n%s", i+2, rendered)
		}
	}
}
