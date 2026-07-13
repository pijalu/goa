// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// TestFilmstrip_ToolResultWithDiagnostics verifies that a tool result carrying
// an LSP diagnostics block (as the write/edit tools now append) renders in the
// chat viewport without breaking the spinner lifecycle. Regression guard for
// the "surface LSP diagnostics" change.
func TestFilmstrip_ToolResultWithDiagnostics(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{
		Type:      agentic.EventToolCall,
		State:     agentic.StateToolCall,
		ToolName:  "write",
		ToolInput: `{"path":"main.go","content":"package main"}`,
		ToolCallID: "c1",
	})
	// Tool result carries a Diagnostics block (as appended by the write/edit
	// tools after querying gopls).
	resultText := "[write: main.go] ✓ Written — 12 bytes, 1 lines\n" +
		"Diagnostics (gopls):\n" +
		"  main.go:3:1: error: undefined: x\n"
	sc.apply(&agentic.OutputEvent{
		Type:       agentic.EventToolResult,
		State:      agentic.StateToolResult,
		ToolName:   "write",
		ToolCallID: "c1",
		Text:       resultText,
	})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	// Canonical invariant: the spinner must never go dark between the first
	// event and the true turn end.
	frames := sc.filmstrip().Frames()
	for i, s := range frames {
		if i == len(frames)-1 {
			continue // final true turn end clears the spinner
		}
		if s.Diff.StatusText == "" {
			t.Errorf("step %d (%s): spinner went dark mid-turn; trace=%v", i, s.Label, sc.filmstrip().StatusTrace())
		}
	}

	// The diagnostics-laden tool result must not break rendering: the tool
	// widget for the write call is present, and the turn ends cleanly (final
	// frame has no spinner).
	rendered := sc.filmstrip().Render()
	if !strings.Contains(rendered, "write main.go") {
		t.Errorf("expected write tool widget in rendered transcript, got:\n%s", rendered)
	}
	last := frames[len(frames)-1]
	if strings.Contains(last.Diff.StatusText, "Thinking") || strings.Contains(last.Diff.StatusText, "request") {
		t.Errorf("spinner still active after EventEnd: %q", last.Diff.StatusText)
	}
}

