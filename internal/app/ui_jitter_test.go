// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// editorY returns the input editor's Y position in the frame, or -1 if absent.
func editorY(s *uiScenario) int {
	frame := s.engine.AgentFrame()
	if n := frame.FindNode("Editor"); n != nil {
		return n.Rect.Y
	}
	return -1
}

// TestUI_NoJitter_StatusToggleDoesNotShiftInput reproduces Bug 2: the input
// editor's Y must not jump when the status spinner is shown (turn active) vs
// cleared (turn end). A height-changing StatusMsg.Render pushes the editor
// up/down by the status height.
func TestUI_NoJitter_StatusToggleDoesNotShiftInput(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// Turn starts: status spinner shown ("Thinking...").
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	yWhileActive := editorY(sc)

	// Turn ends: status cleared (SessionEnd).
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})
	yAtEnd := editorY(sc)

	if yWhileActive < 0 || yAtEnd < 0 {
		t.Fatalf("editor not found: active=%d end=%d", yWhileActive, yAtEnd)
	}
	if yWhileActive != yAtEnd {
		t.Errorf("input editor Y shifted between active (%d) and turn-end (%d) — status toggle jitter; want equal",
			yWhileActive, yAtEnd)
	}
}

// TestUI_NoJitter_NoExcessiveFullRedrawsDuringStreaming verifies Bug 2 point 2:
// a streaming sequence (no terminal size change) must not trigger a full
// screen redraw per event. The compositor should diff incrementally; a full
// wipe should happen only on the first frame (and size changes).
func TestUI_NoJitter_NoExcessiveFullRedrawsDuringStreaming(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// First event primes the first-frame full redraw.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	baseline := sc.engine.FullRedrawCount()

	// Stream content + a tool call/result + more content + turn end. No size
	// change, so none of these should require a full screen wipe.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "Hello"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: " world"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall, ToolName: "read", ToolInput: `{"path":"x"}`, ToolCallID: "c1"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, ToolName: "read", ToolCallID: "c1", Text: "contents"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: " done"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	extra := sc.engine.FullRedrawCount() - baseline
	// Allow a small margin for a viewport-scroll boundary, but it must NOT be
	// one full redraw per streaming event.
	if extra > 1 {
		t.Errorf("streaming triggered %d extra full redraws (baseline=%d after=%d); want <=1", extra, baseline, sc.engine.FullRedrawCount())
	}
}

// TestUI_NoJitter_TabSwitchNoExcessiveFullRedraw verifies Bug 2 point 3: switching
// from the Conversation tab to the Stats tab changes which layers are visible
// (chat suppressed, AgentContent shown) and may shrink the canvas. This must
// not trigger a full-screen wipe (\x1b[2J) — the compositor should diff and
// clear only the stale trailing rows.
func TestUI_NoJitter_TabSwitchNoExcessiveFullRedraw(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()
	sc.applyOrchEvents(lifecycleEvents())
	sc.engine.RenderNow()
	baseline := sc.engine.FullRedrawCount()

	if !sc.app.selectAgentTab("stats") {
		t.Fatal("selectAgentTab(stats) failed")
	}
	sc.engine.RenderNow()
	delta := sc.engine.FullRedrawCount() - baseline
	if delta > 1 {
		t.Errorf("tab switch Conversation->Stats triggered %d full redraws (tearing); want <=1", delta)
	}
}

// TestUI_NoJitter_TabSwitchFromTallChat probes the clearOnShrink path: switching
// from a tall Conversation (long chat) to the shorter Stats tab shrinks the
// canvas. This must not trigger a full-screen wipe (\x1b[2J) — the compositor
// should clear only the stale trailing rows.
func TestUI_NoJitter_TabSwitchFromTallChat(t *testing.T) {
	sc := newOrchViewScenario(t, 100, 30)
	sc.app.attachOrchView(newFakeOrchSource())
	sc.flush()
	sc.applyOrchEvents(lifecycleEvents())
	// Grow the conversation so the Conversation canvas is much taller than Stats.
	for i := 0; i < 40; i++ {
		sc.chat.AddSystemMessage("line " + strings.Repeat("x", 60))
	}
	sc.engine.RenderNow()
	baseline := sc.engine.FullRedrawCount()

	if !sc.app.selectAgentTab("stats") {
		t.Fatal("selectAgentTab(stats) failed")
	}
	sc.engine.RenderNow()
	delta := sc.engine.FullRedrawCount() - baseline
	if delta > 1 {
		t.Errorf("tab switch from tall chat triggered %d full redraws (clearOnShrink tearing); want <=1", delta)
	}
}
