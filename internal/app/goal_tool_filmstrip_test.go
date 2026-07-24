// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// TestFilmstrip_GoalToolCallRenders verifies the unified `goal` tool (bugs.md
// S2) renders a widget in the chat when the model calls it, and that the
// spinner lifecycle is not broken. Regression for the goal-tool consolidation.
func TestFilmstrip_GoalToolCallRenders(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		State:      agentic.StateToolCall,
		ToolName:   "goal",
		ToolInput:  `{"action":"update","status":"complete"}`,
		ToolCallID: "g1",
	})
	sc.apply(&agentic.OutputEvent{
		Type:       agentic.EventToolResult,
		State:      agentic.StateToolResult,
		ToolName:   "goal",
		ToolCallID: "g1",
		Text:       "Goal marked complete.",
	})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	// The spinner must never go dark between the first event and true turn end.
	frames := sc.filmstrip().Frames()
	for i, s := range frames {
		if i == len(frames)-1 {
			continue
		}
		if s.Diff.StatusText == "" {
			t.Errorf("step %d (%s): spinner went dark mid-turn; trace=%v", i, s.Label, sc.filmstrip().StatusTrace())
		}
	}

	rendered := sc.filmstrip().Render()
	if !strings.Contains(rendered, "goal") {
		t.Errorf("expected goal tool widget in rendered transcript, got:\n%s", rendered)
	}
}
