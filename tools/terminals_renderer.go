// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

// TerminalsRenderer displays terminals tool calls and output.
type TerminalsRenderer struct{}

var _ tuirender.ToolRenderer = (*TerminalsRenderer)(nil)

func (TerminalsRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	action := stringArg(args, "action")
	switch action {
	case "open":
		typ := stringArg(args, "type")
		if typ == "" {
			typ = "shell"
		}
		return rToolTitle("Open " + typ + " terminal")
	case "close":
		return rToolTitle("Close terminal " + stringArg(args, "sessionId"))
	case "list":
		return rToolTitle("List terminals")
	case "read":
		return rToolTitle("Read terminal " + stringArg(args, "sessionId"))
	case "send":
		text := stringArg(args, "text")
		// Width-based, cluster-safe cut — a byte cut can split a rune.
		if ansi.Width(text) > 60 {
			text = ansi.Truncate(text, 57) + "..."
		}
		if text == "" {
			text = "..."
		}
		return rBashPrompt("$ ") + rToolTitle(text)
	case "signal":
		return rToolTitle("Signal terminal " + stringArg(args, "sessionId") + " " + stringArg(args, "signal"))
	default:
		return rToolTitle("Terminals")
	}
}

func (TerminalsRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}
	return rToolOutput(output)
}

func (TerminalsRenderer) PreviewLines() int             { return 20 }
func (TerminalsRenderer) HideResultWhenCollapsed() bool { return false }

// DefaultBackground returns false so terminals output uses status-based
// background colors (green on success, red on error, amber while running).
func (TerminalsRenderer) DefaultBackground() bool { return false }
