// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tui"
)

// TestUIScenario_SpinnerSurvivesToolCallTurn is the end-to-end regression
// test for the "spinner disappears after the first tool call" bug, driven
// through the app event layer with a tui.Filmstrip recording.
//
// The exported diagnostic bundle (.goa/exports/goa-export-20260705-151429.zip)
// showed the real-world event sequence that triggered the bug:
//
//	state_change/thinking -> content(thinking) -> tool_call(read) ->
//	tool_result(read) -> [mid-turn end] -> progress -> content(answering)
//
// The mid-turn EventEnd (emitted by the agent after the tool batch) armed the
// status spinner's session-ended guard, so every subsequent Show() was a
// silent no-op and the spinner stayed dark for the rest of the turn. The
// agent-layer fix (TestAgent_SingleEventEndAcrossToolCallTurn) removes that
// spurious EventEnd; this test asserts the user-visible consequence at the
// app/TUI layer: the spinner stays visible from the first activity through
// the final answer, and is only cleared at the true turn end.
//
// Beyond this specific bug, the test demonstrates the agent-testable TUI
// pattern: a model + filmstrip of UI states lets an agent fully "view" the UI
// evolution as data, without a real terminal.
func TestUIScenario_SpinnerSurvivesToolCallTurn(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// The agent emits a thinking state change as the model reasons.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	// Reasoning content streams in.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "I should read the file."})
	// First tool call.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall, ToolName: "read", ToolInput: `{"path":"README.md"}`, ToolCallID: "call_1"})
	// Tool result arrives.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult, ToolName: "read", ToolCallID: "call_1", Text: "read file README.md:1:5"})
	// With the agent-layer fix there is NO mid-turn EventEnd here. The model
	// is queried again and produces the final answer.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "Here is the summary."})
	// True turn end.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	film := sc.filmstrip()
	trace := film.StatusTrace()
	t.Logf("status trace: %v", trace)
	t.Logf("filmstrip:\n%s", film.Render())

	// Invariant: the spinner must be visible (non-empty status text) at every
	// step EXCEPT the final true end. It must never go dark mid-turn.
	frames := film.Frames()
	for i, s := range frames {
		isLast := i == len(frames)-1
		if isLast {
			if s.Diff.StatusText != "" {
				t.Errorf("step %d (%s): expected spinner cleared at true turn end, got %q", i, s.Label, s.Diff.StatusText)
			}
			continue
		}
		if s.Diff.StatusText == "" {
			t.Errorf("step %d (%s): spinner went dark mid-turn; the activity indicator must stay visible across the whole turn. Full trace: %v",
				i, s.Label, trace)
		}
	}

	// Spot-check key lifecycle labels made it to the UI.
	wantContains := []string{"Thinking", "Tool calling"}
	joined := strings.Join(trace, "|")
	for _, w := range wantContains {
		if !strings.Contains(joined, w) {
			t.Errorf("expected status trace to contain %q, got %v", w, trace)
		}
	}

	// The final answer must be present in the last visible frame's chat content.
	last := film.Last()
	if last == nil {
		t.Fatal("expected at least one filmstrip frame")
	}
	visible := strings.Join(last.Frame.Visible, "\n")
	if !strings.Contains(visible, "Here is the summary.") {
		t.Errorf("expected final answer in visible viewport, got:\n%s", visible)
	}
}

// TestUIScenario_StatusTrace is a focused, fast check on the status lifecycle
// captured by the filmstrip. It is the minimal reproduction an agent can run
// to verify the spinner lifecycle for any change to the event->status wiring.
// It also exercises the scenario's direct status helpers (statusVisible /
// statusText), which are the most concise assertion API for activity state.
func TestUIScenario_StatusTrace(t *testing.T) {
	sc := newUIScenario(t, 80, 24)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateContent})
	trace := sc.filmstrip().StatusTrace()

	if len(trace) != 1 || !strings.Contains(trace[0], "Answering") {
		t.Fatalf("expected single 'Answering' status, got %v", trace)
	}
	if !sc.statusVisible() {
		t.Error("statusVisible() = false after content state change, want true")
	}
	if !strings.Contains(sc.statusText(), "Answering") {
		t.Errorf("statusText() = %q, want it to contain Answering", sc.statusText())
	}
}

// TestUIScenario_FilmstripIsANSIFreeForAgentIntrospection ensures the
// filmstrip text rendering is ANSI-free and human/agent-readable, so an AI
// agent can consume it directly when reasoning about UI state.
func TestUIScenario_FilmstripIsANSIFreeForAgentIntrospection(t *testing.T) {
	sc := newUIScenario(t, 80, 24)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall, ToolName: "bash", ToolInput: `{"command":"ls"}`})

	out := sc.filmstrip().Render()
	if strings.ContainsAny(out, "\x1b[") {
		t.Errorf("filmstrip Render() must be ANSI-free for agent consumption, got escape sequences:\n%s", out)
	}
	if !strings.Contains(out, "tool_call/bash") && !strings.Contains(out, "Tool calling") {
		t.Errorf("filmstrip Render() should mention the tool step, got:\n%s", out)
	}
}

// Compile-time assertion that the harness exposes the tui.Filmstrip type so
// external tooling can depend on it.
var _ *tui.Filmstrip = (*tui.Filmstrip)(nil)
