// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/spinner"
	"github.com/pijalu/goa/tui"
)

// TestPythonToolUI_ShowsScriptDuringStreamingAndOutputAfterResult is a
// filmstrip-style regression for the python tool renderer. It asserts that:
//  1. The tool name "python" is clearly visible in the header.
//  2. While the tool-call arguments stream, the body shows the code being
//     written (first 5 lines + line-count hint).
//  3. After the tool result arrives, the body switches to the captured
//     output (last lines, with an earlier-lines hint when truncated).
func TestPythonToolUI_ShowsScriptDuringStreamingAndOutputAfterResult(t *testing.T) {
	_, def := spinner.Default()
	tui.SetSpinner(def)
	defer tui.SetSpinner(spinner.Definition{})

	sc := newUIScenario(t, 100, 24)

	code := "def add(a, b):\n    return a + b\n\nresult = add(2, 3)\nprint(result)\n"
	codeJSON := fmt.Sprintf(`{"code":%q}`, code)

	// First chunk of streamed tool-call arguments.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "python", ToolInput: `{"code":"def add(a, b):`, ToolCallID: "py1",
		IsDelta: true,
	})
	visible := strings.Join(sc.engine.AgentFrame().Visible, "\n")
	stripped := ansi.Strip(visible)
	if !strings.Contains(stripped, "python") {
		t.Errorf("expected 'python' label visible during streaming; visible:\n%s", visible)
	}
	if !strings.Contains(stripped, "def add") {
		t.Errorf("expected streamed code body visible; visible:\n%s", visible)
	}

	// Complete tool-call arguments.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "python", ToolInput: codeJSON, ToolCallID: "py1",
	})
	visible = strings.Join(sc.engine.AgentFrame().Visible, "\n")
	stripped = ansi.Strip(visible)
	if !strings.Contains(stripped, "def add(a, b):") {
		t.Errorf("expected script body visible; visible:\n%s", visible)
	}
	if !strings.Contains(stripped, ">>>") {
		t.Errorf("expected python prompt visible; visible:\n%s", visible)
	}

	// Final result: body switches to output.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "python", ToolCallID: "py1", Text: "5\n",
	})
	visible = strings.Join(sc.engine.AgentFrame().Visible, "\n")
	stripped = ansi.Strip(visible)
	if !strings.Contains(stripped, "5") {
		t.Errorf("expected captured output visible; visible:\n%s", visible)
	}
}
