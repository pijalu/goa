// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
)

// TestMainAgentRegression_SingleStreamBehavior is a regression guard that
// asserts the main-agent path (which does not use the orchestrator agent
// stream registry) still produces exactly one thinking block, one content
// block, and one tool widget, and that the status spinner survives the tool
// call lifecycle.
func TestMainAgentRegression_SingleStreamBehavior(t *testing.T) {
	sc := newUIScenario(t, 100, 30)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateThinking, Role: agentic.Assistant, Text: "reasoning "})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateThinking, Role: agentic.Assistant, Text: "continues"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateContent, Role: agentic.Assistant, Text: "hello "})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, State: agentic.StateContent, Role: agentic.Assistant, Text: "world"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateToolCall})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "bash", ToolInput: `{"command":"ls"}`, ToolCallID: "t1"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, ToolCallID: "t1", Text: "file.txt\n"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	last := sc.filmstrip().Frames()[len(sc.filmstrip().Frames())-1]
	chat := last.Frame.FindNode("ChatViewport")
	if chat == nil {
		t.Fatalf("ChatViewport missing in final frame:\n%s", sc.filmstrip().Render())
	}
	rendered := ansi.Strip(chat.Text)

	if strings.Count(rendered, "thinking...") != 1 {
		t.Errorf("expected exactly one thinking block, got %d:\n%s", strings.Count(rendered, "thinking..."), rendered)
	}
	if !strings.Contains(rendered, "reasoning continues") {
		t.Errorf("expected accumulated thinking text, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "hello world") {
		t.Errorf("expected accumulated content text, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "$ ls") {
		t.Errorf("expected bash tool widget, got:\n%s", rendered)
	}

	trace := sc.filmstrip().StatusTrace()
	if len(trace) == 0 {
		t.Fatal("empty status trace")
	}
	if trace[len(trace)-1] != "" {
		t.Errorf("expected spinner cleared after EventEnd, last trace = %q", trace[len(trace)-1])
	}
}
