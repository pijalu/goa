// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/tuirender"
)

// PythonRenderer displays python tool calls and their captured output.
type PythonRenderer struct {
	KeyExpand string
}

var _ tuirender.ToolRenderer = (*PythonRenderer)(nil)

// NewPythonRenderer returns a renderer for the python tool.
func NewPythonRenderer() *PythonRenderer {
	return &PythonRenderer{KeyExpand: "Ctrl+O"}
}

const (
	pythonHeaderPrompt = ">>> "
	pythonBodyPrompt   = ">>> "
	pythonContPrompt   = "... "
)

// RenderCall shows the clear tool name "python" in the header. The actual
// executed script is rendered in the body by RenderResult.
func (r *PythonRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	return rBashPrompt(pythonHeaderPrompt) + rToolTitle("python")
}

// RenderResult shows the script that was executed (with REPL-style prompts) and
// the captured output. When collapsed, up to 5 script lines and the last 5
// output lines are shown; expanding reveals the full script and full output.
func (r *PythonRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	code := stringArg(ctx.Args, "code")
	if code == "" {
		return ""
	}
	scriptLines := trimTrailingEmptyLines(strings.Split(code, "\n"))
	if len(scriptLines) == 0 {
		return ""
	}

	scriptDisplay, scriptHidden := limitHead(scriptLines, 5, ctx.Expanded)
	outLines := []string{}
	outHidden := 0
	if output != "" {
		outLines = trimTrailingEmptyLines(strings.Split(output, "\n"))
		outLines, outHidden = limitTail(outLines, 5, ctx.Expanded)
	}

	var b strings.Builder
	r.writeScriptLines(&b, scriptDisplay)
	r.writeOutputLines(&b, outLines)
	r.writeExpandHint(&b, scriptHidden+outHidden)
	return b.String()
}

func (r *PythonRenderer) writeScriptLines(b *strings.Builder, lines []string) {
	for i, line := range lines {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		prompt := pythonBodyPrompt
		if i > 0 {
			prompt = pythonContPrompt
		}
		b.WriteString(rBashPrompt(prompt))
		b.WriteString(HighlightLine(line, "python"))
	}
}

func (r *PythonRenderer) writeOutputLines(b *strings.Builder, lines []string) {
	for _, line := range lines {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(rToolOutput(line))
	}
}

func (r *PythonRenderer) writeExpandHint(b *strings.Builder, hidden int) {
	if hidden <= 0 {
		return
	}
	b.WriteByte('\n')
	b.WriteString(rMuted(fmt.Sprintf("... (%d more line(s), %s to expand)", hidden, r.keyExpand())))
}

func limitHead(lines []string, max int, expanded bool) ([]string, int) {
	if expanded || len(lines) <= max {
		return lines, 0
	}
	return lines[:max], len(lines) - max
}

func limitTail(lines []string, max int, expanded bool) ([]string, int) {
	if expanded || len(lines) <= max {
		return lines, 0
	}
	return lines[len(lines)-max:], len(lines) - max
}

func (r *PythonRenderer) keyExpand() string {
	if r.KeyExpand != "" {
		return r.KeyExpand
	}
	return "Ctrl+O"
}

// PreviewLines returns the default number of lines to show when collapsed.
func (r *PythonRenderer) PreviewLines() int { return 12 }

// HideResultWhenCollapsed reports whether collapsed results are hidden.
func (r *PythonRenderer) HideResultWhenCollapsed() bool { return false }
