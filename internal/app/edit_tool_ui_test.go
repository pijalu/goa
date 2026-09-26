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

// TestEditToolUI_ShowsFileNameForBatchCallAndResult is a filmstrip-style
// regression for bugs.md BUG-4 ("edit tool … do not show file names"):
//  1. A batch edit call (path nested in edits[], the shape models commonly
//     emit — no top-level "path") must render "edit <path>" on the widget
//     header, not the "edit ..." placeholder.
//  2. The rendered diff result must keep the "[edit: <path>] N edits applied"
//     summary line, which used to be cut off with everything above the first
//     @@ hunk.
func TestEditToolUI_ShowsFileNameForBatchCallAndResult(t *testing.T) {
	_, def := spinner.Default()
	tui.SetSpinner(def)
	defer tui.SetSpinner(spinner.Definition{})

	sc := newUIScenario(t, 100, 24)

	// Complete batch tool-call arguments: path ONLY inside edits[].
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolCall, State: agentic.StateToolCall,
		ToolName: "edit", ToolCallID: "e1",
		ToolInput: `{"edits":[` +
			`{"path":"internal/app/bootstrap.go","old_string":"old","new_string":"new"},` +
			`{"path":"internal/app/bootstrap.go","operation":"delete_lines","start_line":9,"end_line":10}]}`,
	})
	visible := strings.Join(sc.engine.AgentFrame().Visible, "\n")
	stripped := ansi.Strip(visible)
	if !strings.Contains(stripped, "internal/app/bootstrap.go") {
		t.Errorf("batch edit call must name the target file on the widget; visible:\n%s", stripped)
	}
	if strings.Contains(stripped, "edit ...") {
		t.Errorf("batch edit call must not fall back to the ... placeholder; visible:\n%s", stripped)
	}

	// Result: the summary header above the diff hunk carries the path too.
	sc.apply(&agentic.OutputEvent{
		Type: agentic.EventToolResult, State: agentic.StateToolResult,
		ToolName: "edit", ToolCallID: "e1",
		Text: "[edit: internal/app/bootstrap.go] 2 edits applied — match: exact match\n" +
			"@@ -1,3 +1,3 @@\n package app\n-old line\n+new line\n",
	})
	visible = strings.Join(sc.engine.AgentFrame().Visible, "\n")
	stripped = ansi.Strip(visible)
	if !strings.Contains(stripped, "[edit: internal/app/bootstrap.go] 2 edits applied") {
		t.Errorf("rendered diff must keep the [edit: <path>] result header; visible:\n%s", stripped)
	}
}
