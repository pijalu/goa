// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"strings"

	"github.com/pijalu/goa/internal/tuirender"
)

// PythonRenderer displays python tool calls and their captured output.
type PythonRenderer struct{}

var _ tuirender.ToolRenderer = (*PythonRenderer)(nil)

// RenderCall shows the first line of the Python code being executed.
func (PythonRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	code := stringArg(args, "code")
	line := "python"
	if code != "" {
		first := strings.Split(code, "\n")[0]
		if len(first) > 60 {
			first = first[:57] + "..."
		}
		line = first
	}
	return rBashPrompt(">>> ") + rToolTitle(line)
}

// RenderResult returns the captured interpreter output.
func (PythonRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}
	return rToolOutput(output)
}

// PreviewLines returns the number of output lines to show when collapsed.
func (PythonRenderer) PreviewLines() int { return 12 }

// HideResultWhenCollapsed reports whether collapsed results are hidden.
func (PythonRenderer) HideResultWhenCollapsed() bool { return false }
