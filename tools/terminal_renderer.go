// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

// TerminalRenderer displays terminal tool calls and output.
type TerminalRenderer struct{}

var _ tuirender.ToolRenderer = (*TerminalRenderer)(nil)

func (TerminalRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	cmd := stringArg(args, "command")
	if cmd == "" {
		cmd = "..."
	}
	if len(cmd) > 60 {
		cmd = cmd[:57] + "..."
	}
	return rBashPrompt("$ ") + rToolTitle(cmd)
}

func (TerminalRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}
	return rToolOutput(output)
}

func (TerminalRenderer) PreviewLines() int             { return 20 }
func (TerminalRenderer) HideResultWhenCollapsed() bool { return false }

// DefaultBackground returns false so terminal output uses status-based
// background colors (green on success, red on error, amber while running).
func (TerminalRenderer) DefaultBackground() bool { return false }
