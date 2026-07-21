// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"sync/atomic"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// TestEscapeReachesInterruptWhileToolRuns is the bugs.md regression for
// "ESC must cancel a running tool exec/bash": while a bash tool call is in
// flight (widget running, spinner active), pressing ESC must reach the app's
// escape handler — i.e. the key is NOT swallowed by the running-tool state,
// the chat viewport, or any overlay. The downstream kill mechanics
// (Interrupt → ctx cancel → killBashProcessTree) are covered separately by
// tools/bash_test.go TestBashTool_ExecuteContext_CancelInterruptsLongCommand.
func TestEscapeReachesInterruptWhileToolRuns(t *testing.T) {
	sc := newUIScenario(t, 100, 24)

	// Wire OnEscape exactly as attachInputHandlers does in production:
	// handleEscape calls AgentManager.Interrupt. Here the spy stands in for
	// the agent manager so the harness needs no live provider.
	var escaped atomic.Int32
	sc.editor.OnEscape = func() { escaped.Add(1) }

	// Drive the app into a running-bash state: thinking → tool_call (running).
	sc.apply(&agentic.OutputEvent{Type: agentic.EventStateChange, State: agentic.StateThinking})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "bash", ToolInput: `{"command":"sleep 300"}`, ToolCallID: "c1"})
	if !sc.statusVisible() {
		t.Fatalf("spinner should be visible while the tool runs")
	}

	// Press ESC on the engine (routes through the focused editor, as in
	// production where the editor keeps focus during a tool run).
	sc.engine.SendKey("escape")

	if got := escaped.Load(); got != 1 {
		t.Fatalf("ESC during a running tool must reach the escape handler exactly once, got %d", got)
	}
}
