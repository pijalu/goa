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

// toolStatusSummary enumerates all tool widgets in the chat viewport and
// returns how many are stuck in a non-terminal state (Pending/Running) plus
// the per-widget status list, for readable failure output.
func toolStatusSummary(t *testing.T, sc *uiScenario) (stuck int, statuses []tui.ToolStatus) {
	t.Helper()
	for _, c := range sc.chat.Children() {
		tc, ok := c.(*tui.ToolExecutionComponent)
		if !ok {
			continue
		}
		statuses = append(statuses, tc.Status())
		if tc.Status() == tui.ToolPending || tc.Status() == tui.ToolRunning {
			stuck++
		}
	}
	return stuck, statuses
}

func statusName(s tui.ToolStatus) string {
	switch s {
	case tui.ToolPending:
		return "Pending"
	case tui.ToolRunning:
		return "Running"
	case tui.ToolSuccess:
		return "Success"
	case tui.ToolError:
		return "Error"
	}
	return "Unknown"
}

// TestToolStreamingRepro_PartialEmptyIDThenFinalRealID reproduces the
// "stuck on write" bug: a provider streams tool-call partials with an EMPTY
// ToolCallID (start/delta events often omit the id), then emits the completed
// call with the real id, then the result with the real id. The orphaned
// streaming widget (created with empty id → single-slot activeTool) never
// receives the transition and stays Pending forever.
//
// No EventEnd is sent (the real session continued with more tool calls), so
// failPendingTools does not mask the orphan.
func TestToolStreamingRepro_PartialEmptyIDThenFinalRealID(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// Streaming partials: empty ToolCallID (provider omits id on start/delta).
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		IsDelta: true, ToolName: "write", ToolInput: `{"content":"package main`, ToolCallID: ""})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		IsDelta: true, ToolName: "write", ToolInput: `{"content":"package main\nfunc main(){}", "path":"main.go"}`, ToolCallID: ""})

	// Completed (non-delta) call: real id now arrives.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		IsDelta: false, ToolName: "write", ToolInput: `{"content":"package main\nfunc main(){}", "path":"main.go"}`, ToolCallID: "real-write-id"})

	// Result with the real id.
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "write", ToolCallID: "real-write-id", Text: "[write: main.go]\nwrote 2 lines"})

	// Inspect BEFORE EventEnd: the orphan must not be hidden by failPendingTools.
	_, statuses := toolStatusSummary(t, sc)
	names := make([]string, len(statuses))
	for i, s := range statuses {
		names[i] = statusName(s)
	}
	// Exactly ONE widget should exist and it must be Success (no orphan).
	if len(statuses) != 1 {
		t.Fatalf("expected exactly 1 write widget, got %d; statuses=%v", len(statuses), names)
	}
	if statuses[0] != tui.ToolSuccess {
		t.Fatalf("expected the single widget to be Success, got %v (orphaned streaming widget)", names)
	}
}

// TestToolStreamingRepro_PartialRealIDThenFinalRealID is the baseline where
// partials carry the same id as the final call. This path should already work.
func TestToolStreamingRepro_PartialRealIDThenFinalRealID(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		IsDelta: true, ToolName: "write", ToolInput: `{"content":"package main`, ToolCallID: "w1"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		IsDelta: false, ToolName: "write", ToolInput: `{"content":"package main\nfunc main(){}", "path":"main.go"}`, ToolCallID: "w1"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "write", ToolCallID: "w1", Text: "[write: main.go]\nwrote 2 lines"})

	_, statuses := toolStatusSummary(t, sc)
	names := make([]string, len(statuses))
	for i, s := range statuses {
		names[i] = statusName(s)
	}
	if len(statuses) != 1 || statuses[0] != tui.ToolSuccess {
		t.Fatalf("baseline: expected exactly 1 Success widget, got %v", names)
	}
}

// TestToolStreamingRepro_LongRunningProgress confirms that a tool whose
// partial output streams via EventToolProgress (e.g. long bash) eventually
// terminates when its EventToolResult arrives, instead of staying Running.
func TestToolStreamingRepro_LongRunningProgress(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		IsDelta: false, ToolName: "bash", ToolInput: `{"command":"go test ./..."}`, ToolCallID: "b1"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolProgress, State: agentic.StateToolCall,
		ToolName: "bash", ToolCallID: "b1", Text: "ok pkg/a 0.1s\nok pkg/b 0.2s"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolProgress, State: agentic.StateToolCall,
		ToolName: "bash", ToolCallID: "b1", Text: "ok pkg/a 0.1s\nok pkg/b 0.2s\nok pkg/c 0.3s"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "bash", ToolCallID: "b1", Text: "ok pkg/a 0.1s\nok pkg/b 0.2s\nok pkg/c 0.3s"})

	_, statuses := toolStatusSummary(t, sc)
	names := make([]string, len(statuses))
	for i, s := range statuses {
		names[i] = statusName(s)
	}
	if len(statuses) != 1 || statuses[0] != tui.ToolSuccess {
		t.Fatalf("progress: expected exactly 1 Success widget, got %v", names)
	}
}

// renderToolBody returns the ANSI-stripped body of the single tool widget in
// the scenario, for asserting preview/stat content.
func renderToolBody(t *testing.T, sc *uiScenario) string {
	t.Helper()
	for _, c := range sc.chat.Children() {
		tc, ok := c.(*tui.ToolExecutionComponent)
		if !ok {
			continue
		}
		return ansi.Strip(strings.Join(tc.Render(80), "\n"))
	}
	return ""
}

// TestToolStreaming_ShowInfoImmediately verifies G1/G2: a streaming write
// shows its (partial) content in the widget body BEFORE the call completes —
// the body is non-empty while the widget is still Pending.
func TestToolStreaming_ShowInfoImmediately(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		IsDelta: true, ToolName: "write",
		ToolInput:  `{"content":"package main\n\nfunc main(){}","path":"m.go"}`, ToolCallID: "w1"})

	// While still streaming (Pending), the body must already show content.
	body := renderToolBody(t, sc)
	if !strings.Contains(body, "package main") {
		t.Fatalf("expected streaming write body to show content immediately, got:\n%s", body)
	}
}

// TestToolStreaming_LiveLineCounter verifies G4: a long streaming input shows
// a live line counter ("streaming… N lines in").
func TestToolStreaming_LiveLineCounter(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	content := "a\nb\nc\nd\ne\n"
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		IsDelta: true, ToolName: "write",
		ToolInput: `{"content":"` + content + `","path":"m.go"}`, ToolCallID: "w2"})

	body := renderToolBody(t, sc)
	if !strings.Contains(body, "streaming") || !strings.Contains(body, "lines in") {
		t.Fatalf("expected live line counter while streaming, got:\n%s", body)
	}
}

// TestToolStreaming_GlobalToggleExpand verifies G5: Ctrl+O flips ALL tool
// blocks between Summary and Full for the session.
func TestToolStreaming_GlobalToggleExpand(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		IsDelta: false, ToolName: "bash", ToolInput: `{"command":"seq 1 30"}`, ToolCallID: "b1"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "bash", ToolCallID: "b1", Text: strings.Repeat("line\n", 30)})

	// Summary: body is truncated and the stats line advertises Ctrl+O.
	summary := renderToolBody(t, sc)
	if !strings.Contains(summary, "Ctrl+O to expand") {
		t.Fatalf("summary should advertise Ctrl+O, got:\n%s", summary)
	}

	// Ctrl+O → Full: every widget expands; no more "Ctrl+O to expand" hint.
	sc.engine.SendKey("ctrl+o")
	full := renderToolBody(t, sc)
	if strings.Contains(full, "Ctrl+O to expand") {
		t.Fatalf("after Ctrl+O the expand hint should be gone, got:\n%s", full)
	}
}

// TestToolStreaming_ConfigDefaultFull verifies the config default: when
// tui.tools.view == "full", widgets start expanded.
func TestToolStreaming_ConfigDefaultFull(t *testing.T) {
	sc := newUIScenario(t, 100, 24)
	sc.app.subs.cfg.TUI.Tools.View = "full"
	sc.chat.SetToolsConfig(true, 10)
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		IsDelta: false, ToolName: "bash", ToolInput: `{"command":"seq 1 30"}`, ToolCallID: "b1"})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "bash", ToolCallID: "b1", Text: strings.Repeat("line\n", 30)})

	body := renderToolBody(t, sc)
	if strings.Contains(body, "Ctrl+O to expand") {
		t.Fatalf("config view=full should start expanded (no hint), got:\n%s", body)
	}
}
