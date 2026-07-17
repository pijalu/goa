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

// These tests validate the open bugs.md item:
//
//	"edit/write do not show streaming — only final results are shown"
//
// They drive a realistic streaming tool-call argument sequence (multiple
// EventToolCall deltas whose ToolInput grows the way a provider accumulates
// it) through the full production app event handler and assert that the tool
// widget body updates LIVE as content arrives — not only after the result.
//
// They are the end-to-end counterpart to the unit-level renderer tests: a
// passing renderer test alone does not prove the streamed content reaches the
// screen, because the event→widget wiring (tooltracker → SetArgsPartial →
// buildBody → RenderPartial) must all cooperate.

// visibleText returns the ANSI-stripped visible screen text for assertions.
func visibleText(sc *uiScenario) string {
	frame := sc.engine.AgentFrame()
	return ansi.Strip(strings.Join(frame.Visible, "\n"))
}

// toolWidgets returns the tool-execution widgets currently in the chat
// viewport, in insertion order. Used to assert widget identity/lifecycle
// (no orphaned/stuck widgets).
func toolWidgets(sc *uiScenario) []*tui.ToolExecutionComponent {
	var out []*tui.ToolExecutionComponent
	for _, c := range sc.chat.Children() {
		if tc, ok := c.(*tui.ToolExecutionComponent); ok {
			out = append(out, tc)
		}
	}
	return out
}

// TestBugs_WriteStreamingShowsContentLive replays a streaming write whose
// content grows across several deltas and asserts each increment becomes
// visible in the widget body before the result arrives.
func TestBugs_WriteStreamingShowsContentLive(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})

	// Delta 1: content just starts. Path is closed, content is unterminated
	// (no closing quote) — the realistic mid-stream shape.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "write", ToolCallID: "call_1", IsDelta: true,
		ToolInput: `{"path":"main.go","content":"package main`,
	})
	if got := visibleText(sc); !strings.Contains(got, "package main") {
		t.Errorf("delta 1: expected streamed content 'package main' in body, got:\n%s", got)
	}
	if got := visibleText(sc); !strings.Contains(got, "write main.go") {
		t.Errorf("delta 1: expected write header visible, got:\n%s", got)
	}

	// Delta 2: content grows — a func appears. Must show immediately.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "write", ToolCallID: "call_1", IsDelta: true,
		ToolInput: `{"path":"main.go","content":"package main\n\nfunc alpha() {}`,
	})
	if got := visibleText(sc); !strings.Contains(got, "func alpha()") {
		t.Errorf("delta 2: expected streamed 'func alpha()' in body, got:\n%s", got)
	}

	// Delta 3: content grows past the default preview window (10 lines). The
	// head must still be visible and the truncation hint must reflect growth.
	longContent := "package main\n\n" +
		"func alpha() {}\nfunc beta() {}\nfunc gamma() {}\nfunc delta() {}\n" +
		"func epsilon() {}\nfunc zeta() {}\nfunc eta() {}\nfunc theta() {}\n" +
		"func iota() {}\nfunc kappa() {}\nfunc lambda() {}"
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "write", ToolCallID: "call_1", IsDelta: true,
		ToolInput: `{"path":"main.go","content":"` + longContent,
	})
	got := visibleText(sc)
	if !strings.Contains(got, "func beta()") {
		t.Errorf("delta 3: expected head content 'func beta()' visible, got:\n%s", got)
	}
	if !strings.Contains(got, "more lines") {
		t.Errorf("delta 3: expected truncation hint once content exceeds preview, got:\n%s", got)
	}

	// Finalize args, then deliver the result and end the turn.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "write", ToolCallID: "call_1",
		ToolInput: `{"path":"main.go","content":"` + longContent + `"}`,
	})
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "write", ToolCallID: "call_1",
		Text: "[write: main.go]\n✓ Written\n```\npackage main\n```\n",
	})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	got = visibleText(sc)
	if !strings.Contains(got, "package main") {
		t.Errorf("after result: expected content still visible, got:\n%s", got)
	}
}

// TestBugs_EditStreamingShowsDiffstatLive replays a streaming edit whose
// old_string then new_string arrive as deltas, asserting the live diffstat
// ("-X lines, +Y lines") appears and updates before the result.
func TestBugs_EditStreamingShowsDiffstatLive(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})

	// Delta: old_string streaming (2 lines), new_string not yet present.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "edit", ToolCallID: "call_e1", IsDelta: true,
		ToolInput: `{"path":"x.go","old_string":"foo\nbar`,
	})
	got := visibleText(sc)
	if !strings.Contains(got, "-2 lines") {
		t.Errorf("delta (old_string): expected live '-2 lines' diffstat, got:\n%s", got)
	}

	// Delta: new_string now arrives (2 lines).
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "edit", ToolCallID: "call_e1", IsDelta: true,
		ToolInput: `{"path":"x.go","old_string":"foo\nbar","new_string":"foo\nbaz`,
	})
	got = visibleText(sc)
	if !strings.Contains(got, "+2 lines") {
		t.Errorf("delta (new_string): expected live '+2 lines' diffstat, got:\n%s", got)
	}

	// Finalize + result + end.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "edit", ToolCallID: "call_e1",
		ToolInput: `{"path":"x.go","old_string":"foo\nbar","new_string":"foo\nbaz"}`,
	})
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "edit", ToolCallID: "call_e1",
		Text: "[edit: x.go]\n✓ Applied\n```\n@@ -1,2 +1,2 @@\n-foo\n-bar\n+foo\n+baz\n```\n",
	})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	got = visibleText(sc)
	if !strings.Contains(got, "baz") {
		t.Errorf("after result: expected edit diff content visible, got:\n%s", got)
	}
}

// TestBugs_DisconnectSurfacesNotification validates the open bugs.md item
// "disconnection/stop of work — no error/notification". A cancelled stream
// must surface a visible "Generation stopped by user." message, and a stream
// that dies with a connection error must surface a friendly hint — never a
// silent stop.
func TestBugs_DisconnectSurfacesNotification(t *testing.T) {
	t.Run("cancelled surfaces visible notification", func(t *testing.T) {
		sc := newUIScenario(t, 100, 24)
		sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
		sc.apply(&agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateContent, Text: "partial", IsDelta: true})
		sc.apply(&agentic.OutputEvent{
			Type:     agentic.EventEnd,
			Metadata: map[string]string{"cancelled": "true"},
		})
		if got := visibleText(sc); !strings.Contains(got, "stopped by user") {
			t.Errorf("expected visible 'stopped by user' notification after cancel, got:\n%s", got)
		}
	})

	t.Run("connection error surfaces visible hint", func(t *testing.T) {
		sc := newUIScenario(t, 100, 24)
		sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
		sc.apply(&agentic.OutputEvent{
			Type: agentic.EventEnd,
			Text: "connection refused: dial tcp 127.0.0.1:1234",
		})
		if got := visibleText(sc); !strings.Contains(got, "connection error") {
			t.Errorf("expected visible friendly connection hint after error, got:\n%s", got)
		}
	})
}

// TestBugs_NoStuckWriteWidget_LateIDAdoption validates the open bugs.md item
// "Write stuck" on the widget side: when a write streams with an EMPTY
// ToolCallID and the completed call/result later arrives with the real id,
// exactly one widget exists and it resolves to success (not stranded in
// Pending with an elapsed timer running to infinity).
func TestBugs_NoStuckWriteWidget_LateIDAdoption(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})

	// Stream args with NO id (common provider behaviour: id arrives only on
	// the completed call).
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "write", IsDelta: true,
		ToolInput: `{"path":"main.go","content":"package main`,
	})
	// Completed call + result with the real id: late-id adoption must reuse
	// the existing widget, not orphan it.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "write", ToolCallID: "call_real",
		ToolInput: `{"path":"main.go","content":"package main"}`,
	})
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "write", ToolCallID: "call_real",
		Text: "[write: main.go]\n✓ Written\n```\npackage main\n```\n",
	})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	ws := toolWidgets(sc)
	if len(ws) != 1 {
		t.Fatalf("expected exactly 1 write widget (no orphan), got %d", len(ws))
	}
	if ws[0].Status() != tui.ToolSuccess {
		t.Errorf("expected adopted widget to resolve to success, got %v", ws[0].Status())
	}
}

// TestBugs_NoStuckWriteWidget_StrandedMarkedInterrupted validates the other
// "Write stuck" half: if a write streams but never receives a result (the
// turn ends while it is still in-flight, e.g. a mid-tool disconnect), the
// failPendingTools safety net at EventEnd must mark it interrupted (✗) rather
// than leaving it spinning forever.
func TestBugs_NoStuckWriteWidget_StrandedMarkedInterrupted(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "write", ToolCallID: "call_strand",
		ToolInput: `{"path":"main.go","content":"package main"}`,
	})
	// No EventToolResult — the turn ends abruptly.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	ws := toolWidgets(sc)
	if len(ws) != 1 {
		t.Fatalf("expected exactly 1 write widget, got %d", len(ws))
	}
	if ws[0].Status() != tui.ToolError {
		t.Errorf("expected stranded widget to be marked interrupted (error), got %v", ws[0].Status())
	}
	if got := visibleText(sc); !strings.Contains(got, "interrupted") {
		t.Errorf("expected visible '(interrupted)' output for stranded widget, got:\n%s", got)
	}
}

// TestBugs_CanceledMidToolCall_LabeledNeverRan reproduces the bugs.md item
// "Tool call start a review but no output of work done": the model streams an
// agent tool call, the stream is canceled while the arguments are STILL
// streaming (the tool never executes), and the widget must say the tool never
// ran — not a bare "(interrupted)" that implies work happened and its output
// was lost.
func TestBugs_CanceledMidToolCall_LabeledNeverRan(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})

	// Partial agent tool-call deltas — args never complete (unterminated JSON),
	// matching the real export: ~40 "Calling agent..." updates then cancel.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "agent", ToolCallID: "call_agent", IsDelta: true,
		ToolInput: `{"description":"Review render loop + compositor perf","subagent_type":"coder","prompt":"Review`,
	})
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "agent", ToolCallID: "call_agent", IsDelta: true,
		ToolInput: `{"description":"Review render loop + compositor perf","subagent_type":"coder","prompt":"Review the render loop and compositor`,
	})

	// Stream canceled mid-tool-call: turn ends with no result.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	ws := toolWidgets(sc)
	if len(ws) != 1 {
		t.Fatalf("expected exactly 1 agent widget, got %d", len(ws))
	}
	if ws[0].Status() != tui.ToolError {
		t.Errorf("expected canceled widget to be marked error, got %v", ws[0].Status())
	}
	got := visibleText(sc)
	if !strings.Contains(got, "never ran") {
		t.Errorf("expected '(canceled before execution — the tool never ran)' for mid-args cancel, got:\n%s", got)
	}
}

// TestBugs_CanceledRunningTool_LabeledInterrupted is the counterpart: a tool
// whose args DID complete (execution began) but whose result never arrives
// keeps the "(interrupted)" label.
func TestBugs_CanceledRunningTool_LabeledInterrupted(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "agent", ToolCallID: "call_agent2",
		ToolInput: `{"description":"Review render loop","subagent_type":"coder","prompt":"Review it"}`,
	})
	// Turn ends while the (fully-specified) tool is running.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	ws := toolWidgets(sc)
	if len(ws) != 1 {
		t.Fatalf("expected exactly 1 agent widget, got %d", len(ws))
	}
	got := visibleText(sc)
	if !strings.Contains(got, "(interrupted)") {
		t.Errorf("expected '(interrupted)' for a tool canceled while running, got:\n%s", got)
	}
	if strings.Contains(got, "never ran") {
		t.Errorf("a fully-specified tool must NOT be labeled 'never ran', got:\n%s", got)
	}
}

// TestBugs_WriteToolStatsShowsTotal verifies that after a write tool completes,
// the tool widget correctly renders the content.
func TestBugs_WriteToolStatsShowsTotal(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})

	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "write", ToolCallID: "call_write1",
		ToolInput: `{"path":"/tmp/test.txt","content":"line1\nline2\nline3"}`,
	})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventProgress, Text: "Writing..."})
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolResult, State: agentic.StateToolCall,
		ToolName: "write", ToolCallID: "call_write1",
		ToolResult: "[write: /tmp/test.txt]\n✓ Written — 14 bytes, 3 lines\n```\nline1\nline2\nline3\n```\n",
	})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	// Verify the tool widget exists and has the correct output.
	ws := toolWidgets(sc)
	if len(ws) == 0 {
		t.Fatal("expected at least one tool widget")
	}
	last := ws[len(ws)-1]
	if last.Status() != tui.ToolSuccess {
		t.Errorf("expected ToolSuccess, got %v", last.Status())
	}
	got := visibleText(sc)
	if !strings.Contains(got, "write") {
		t.Errorf("expected 'write' in output, got:\n%s", got)
	}
	if strings.Contains(got, "writing") {
		t.Errorf("should NOT show 'writing' after completion, got:\n%s", got)
	}
	// The stats footer must report the TOTAL lines written (3), not a
	// preview count. The retained args are the authoritative content source.
	if !strings.Contains(got, "3 lines") {
		t.Errorf("expected total '3 lines' stat after completion, got:\n%s", got)
	}
}

// TestBugs_ProgressClearClearsStatus validates the bugs.md "stuck in
// sending" fix: the agent's cleanup path emits EventProgress with empty text
// on every turn exit, and the app must translate it into a status-spinner
// clear. Previously handleProgressEvent ignored empty-text events, so the
// clear emission was a no-op and "Sending request..." could linger.
func TestBugs_ProgressClearClearsStatus(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventProgress, Text: "Sending request..."})
	if got := sc.status.Text(); got != "Sending request..." {
		t.Fatalf("precondition: expected status 'Sending request...', got %q", got)
	}

	sc.apply(&agentic.OutputEvent{Type: agentic.EventProgress, Text: ""})
	if got := sc.status.Text(); got != "" {
		t.Errorf("expected status cleared by empty progress event, got %q", got)
	}
}

// TestBugs_ToolCallNoTerminalArtefacts verifies that tool call rendering
// does not contain raw terminal prompt or status bar lines.
func TestBugs_ToolCallNoTerminalArtefacts(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})

	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "write", ToolCallID: "call_write2",
		ToolInput: `{"path":"/tmp/test.txt","content":"package main"}`,
	})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventProgress, Text: "Writing..."})
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolResult, State: agentic.StateToolCall,
		ToolName: "write", ToolCallID: "call_write2",
		ToolResult: "[write: /tmp/test.txt]\n```\npackage main\n```\n",
	})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	ws := toolWidgets(sc)
	if len(ws) == 0 {
		t.Fatal("expected at least one tool widget")
	}

	got := visibleText(sc)
	// The conversation view should NOT contain raw terminal prompt patterns.
	for _, artefact := range []string{"~/dev/goa", "tok/s", "coding-posture"} {
		if strings.Contains(got, artefact) {
			t.Errorf("tool result should not contain terminal artefact %q, got:\n%s", artefact, got)
		}
	}
}
