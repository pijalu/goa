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

const pythonPrompt = ">>> "

// RenderCall shows the python tool name, the first line of the code, and the
// total line count so the header is identifiable even for multi-line scripts.
func (r *PythonRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	code := stringArg(args, "code")
	if code == "" {
		return rBashPrompt(pythonPrompt) + rToolTitle("python")
	}
	lines := strings.Split(code, "\n")
	// Drop trailing blank lines so a single trailing newline doesn't inflate
	// the reported count.
	for len(lines) > 0 && strings.TrimSpace(lines[len(lines)-1]) == "" {
		lines = lines[:len(lines)-1]
	}
	first := strings.TrimSpace(lines[0])
	if len(first) > 60 {
		first = first[:57] + "..."
	}
	lineCount := len(lines)
	var suffix string
	if lineCount > 1 {
		suffix = rMuted(fmt.Sprintf(" (%d lines)", lineCount))
	}
	return rBashPrompt(pythonPrompt) + rToolTitle("python") + " " + rToolOutput(first) + suffix
}

// RenderResult shows the streaming/pending script before execution and the
// captured output afterwards. Output is rendered as the last N lines with an
// expansion hint, matching the bash renderer's pattern.
func (r *PythonRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output != "" {
		return r.renderOutput(output, ctx)
	}
	return r.renderCode(ctx)
}

func (r *PythonRenderer) renderCode(ctx tuirender.RenderContext) string {
	code := stringArg(ctx.Args, "code")
	if code == "" {
		return ""
	}
	lines := strings.Split(code, "\n")
	lines = trimTrailingEmptyLines(lines)
	total := len(lines)
	if total == 0 {
		return ""
	}
	highlighted := HighlightCode(code, "python")
	highlighted = trimTrailingEmptyLines(highlighted)

	maxLines := 5
	if ctx.Expanded {
		maxLines = len(highlighted)
	}
	display := highlighted
	remaining := 0
	if len(highlighted) > maxLines {
		display = highlighted[:maxLines]
		remaining = len(highlighted) - maxLines
	}

	var b strings.Builder
	for _, line := range display {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(line)
	}
	if remaining > 0 {
		b.WriteByte('\n')
		b.WriteString(rMuted(fmt.Sprintf("... (%d more lines, %s to expand)", remaining, r.keyExpand())))
	}
	return b.String()
}

func (r *PythonRenderer) renderOutput(output string, ctx tuirender.RenderContext) string {
	lines := strings.Split(output, "\n")
	lines = trimTrailingEmptyLines(lines)
	if len(lines) == 0 {
		return ""
	}
	maxLines := 5
	if ctx.Expanded {
		maxLines = len(lines)
	}
	display := lines
	hidden := 0
	if len(lines) > maxLines {
		display = lines[len(lines)-maxLines:]
		hidden = len(lines) - maxLines
	}

	var b strings.Builder
	if hidden > 0 {
		b.WriteString(rMuted(fmt.Sprintf("… %d earlier lines (%s to expand)", hidden, r.keyExpand())))
		b.WriteByte('\n')
	}
	for _, line := range display {
		b.WriteString(rToolOutput(line))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r *PythonRenderer) keyExpand() string {
	if r.KeyExpand != "" {
		return r.KeyExpand
	}
	return "Ctrl+O"
}

// PreviewLines returns the default number of lines to show when collapsed.
func (r *PythonRenderer) PreviewLines() int { return 6 }

// HideResultWhenCollapsed reports whether collapsed results are hidden.
func (r *PythonRenderer) HideResultWhenCollapsed() bool { return false }
