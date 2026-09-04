// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// TestLSPToolCallHeaderShowsOperationAndTerm is the terminal-output validation
// for bugs.md B-LSPhdr: it replays the exact lsp tool calls from the reported
// session export (goa-export-20260904-080913) through the production component
// tree on a fake terminal and asserts the rendered header carries the
// operation and the searched term — previously the header showed only the
// path ("✓ lsp cmd/goa/main.go") for every operation.
func TestLSPToolCallHeaderShowsOperationAndTerm(t *testing.T) {
	sc := newUIScenario(t, 120, 30)

	// Exact events from the export: a symbols call followed by a
	// workspaceSymbol search for "AskUserQuestionTool".
	sc.apply(&agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		State:      agentic.StateToolCall,
		ToolName:   "lsp",
		ToolInput:  `{"op":"symbols","path":"cmd/goa/main.go"}`,
		ToolCallID: "lsp-call-1",
	})
	sc.apply(&agentic.OutputEvent{
		Type:       agentic.EventToolResult,
		State:      agentic.StateToolResult,
		ToolName:   "lsp",
		ToolCallID: "lsp-call-1",
		Text:       "Symbols:\n  main (line 1)\n",
	})
	sc.apply(&agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		State:      agentic.StateToolCall,
		ToolName:   "lsp",
		ToolInput:  `{"op":"workspaceSymbol","path":"cmd/goa/main.go","query":"AskUserQuestionTool"}`,
		ToolCallID: "lsp-call-2",
	})
	sc.apply(&agentic.OutputEvent{
		Type:       agentic.EventToolResult,
		State:      agentic.StateToolResult,
		ToolName:   "lsp",
		ToolCallID: "lsp-call-2",
		Text:       "Workspace symbols (2):\n  /x/tools/ask/ask_user.go:40:6\n  /x/tools/ask/ask_user.go:46:2\n",
	})
	sc.apply(&agentic.OutputEvent{Type: agentic.EventEnd})

	// Assert on the actual rendered terminal screen (ANSI-stripped visible
	// frame): every lsp widget header must name the operation and its
	// target. The transcript model only stores the bare tool name, so the
	// rendered frame is the meaningful signal here.
	visible := frameText(sc)
	for _, want := range []string{
		"lsp symbols cmd/goa/main.go",
		`lsp workspaceSymbol "AskUserQuestionTool"`,
	} {
		if !strings.Contains(visible, want) {
			t.Errorf("rendered screen missing header %q\nvisible:\n%s", want, visible)
		}
	}
}
