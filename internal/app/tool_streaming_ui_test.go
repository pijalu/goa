// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/spinner"
	"github.com/pijalu/goa/tui"
)

// TestWriteToolUI_StreamsContentBeforeResult validates that the write tool
// widget shows the streaming file content before the tool result arrives.
func TestWriteToolUI_StreamsContentBeforeResult(t *testing.T) {
	_, def := spinner.Default()
	tui.SetSpinner(def)
	defer tui.SetSpinner(spinner.Definition{})

	sc := newUIScenario(t, 100, 24)

	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "write", ToolInput: `{"path":"main.go","content":"package main`, ToolCallID: "w1",
		IsDelta: true,
	})
	visible := strings.Join(sc.engine.AgentFrame().Visible, "\n")
	stripped := ansi.Strip(visible)
	if !strings.Contains(stripped, "package main") {
		t.Errorf("expected streamed content visible for write; visible:\n%s", visible)
	}
	if !strings.Contains(stripped, "write") {
		t.Errorf("expected 'write' label visible; visible:\n%s", visible)
	}
}

// TestBashToolUI_StreamsCommandHeader validates that the bash tool widget
// updates its header while the command argument streams.
func TestBashToolUI_StreamsCommandHeader(t *testing.T) {
	_, def := spinner.Default()
	tui.SetSpinner(def)
	defer tui.SetSpinner(spinner.Definition{})

	sc := newUIScenario(t, 100, 24)

	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "bash", ToolInput: `{"command":"echo hello`, ToolCallID: "b1",
		IsDelta: true,
	})
	visible := strings.Join(sc.engine.AgentFrame().Visible, "\n")
	stripped := ansi.Strip(visible)
	if !strings.Contains(stripped, "echo") {
		t.Errorf("expected streamed command visible in bash header; visible:\n%s", visible)
	}
	if !strings.Contains(stripped, "bash") {
		t.Errorf("expected 'bash' label visible; visible:\n%s", visible)
	}
}
