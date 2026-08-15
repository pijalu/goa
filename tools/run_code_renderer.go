// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/tuirender"
)

// RunCodeRenderer displays run_code tool calls: the description as the header
// and the program + captured output in the body, mirroring the python
// renderer's layout.
type RunCodeRenderer struct {
	KeyExpand string
}

var _ tuirender.ToolRenderer = (*RunCodeRenderer)(nil)
var _ tuirender.StreamingRenderer = (*RunCodeRenderer)(nil)

// NewRunCodeRenderer returns a renderer for the run_code tool.
func NewRunCodeRenderer() *RunCodeRenderer {
	return &RunCodeRenderer{KeyExpand: KeyExpandLabel}
}

// RenderCall shows the tool name plus the model-authored description.
func (r *RunCodeRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	desc := stringArg(args, "description")
	if desc == "" {
		return rToolTitle("run_code")
	}
	return rToolTitle("run_code") + " — " + rMuted(desc)
}

// RenderResult shows the program that was executed and the captured output,
// mirroring the python renderer.
func (r *RunCodeRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	code := stringArg(ctx.Args, "code")
	if code == "" {
		return ""
	}
	scriptLines := trimTrailingEmptyLines(strings.Split(code, "\n"))
	if len(scriptLines) == 0 {
		return ""
	}

	pv := previewLinesFromCtx(ctx, r.PreviewLines())
	scriptDisplay, scriptHidden := limitHead(scriptLines, pv, ctx.Expanded)
	outLines := []string{}
	outHidden := 0
	if output != "" {
		outLines = trimTrailingEmptyLines(strings.Split(output, "\n"))
		outLines, outHidden = limitTail(outLines, pv, ctx.Expanded)
	}

	var b strings.Builder
	for i, line := range scriptDisplay {
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
	for _, line := range outLines {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(rToolOutput(line))
	}
	if hidden := scriptHidden + outHidden; hidden > 0 {
		b.WriteByte('\n')
		b.WriteString(rMuted(fmt.Sprintf("... (%d more line(s), %s to expand)", hidden, r.keyExpand())))
	}
	return b.String()
}

// RenderPartial implements tuirender.StreamingRenderer: while the run_code
// arguments are still streaming, the body previews the program being written.
func (r *RunCodeRenderer) RenderPartial(args map[string]any, ctx tuirender.RenderContext) string {
	ctx.Args = args
	return r.RenderResult("", ctx)
}

func (r *RunCodeRenderer) keyExpand() string {
	if r.KeyExpand != "" {
		return r.KeyExpand
	}
	return KeyExpandLabel
}

// PreviewLines returns the default number of lines to show when collapsed.
func (r *RunCodeRenderer) PreviewLines() int { return 12 }

// HideResultWhenCollapsed reports whether collapsed results are hidden.
func (r *RunCodeRenderer) HideResultWhenCollapsed() bool { return false }
