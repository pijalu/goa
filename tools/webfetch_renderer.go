// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/tuirender"
)

// WebFetchRenderer renders webfetch tool calls and results.
type WebFetchRenderer struct {
	KeyExpand string
}

var _ tuirender.ToolRenderer = (*WebFetchRenderer)(nil)

// NewWebFetchRenderer creates a new webfetch renderer.
func NewWebFetchRenderer() *WebFetchRenderer {
	return &WebFetchRenderer{KeyExpand: KeyExpandLabel}
}

func (r *WebFetchRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	u := stringArg(args, "url")
	action := stringArg(args, "action")
	if action == "" {
		action = "fetch"
	}
	if u == "" {
		u = "..."
	}
	start := intArg(args, "start_line")
	end := intArg(args, "end_line")
	var suffix string
	if start > 0 && end > 0 {
		suffix = fmt.Sprintf(" (%s:%d:%d)", action, start, end)
	} else {
		suffix = fmt.Sprintf(" (%s)", action)
	}
	return rToolTitle("webfetch ") + rToolOutput(u) + rMuted(suffix)
}

func (r *WebFetchRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}
	if !ctx.Expanded && r.HideResultWhenCollapsed() {
		return ""
	}
	parsed := r.parseOutput(output)
	lines := strings.Split(parsed.body, "\n")
	maxLines := previewLinesFromCtx(ctx, r.PreviewLines())
	if ctx.Expanded {
		maxLines = len(lines)
	}
	displayLines := lines
	if len(lines) > maxLines {
		displayLines = lines[:maxLines]
	}
	hiddenCount := len(lines) - len(displayLines)

	var b strings.Builder
	if parsed.header != "" {
		b.WriteString(rMuted(parsed.header))
		b.WriteByte('\n')
	}
	for _, line := range displayLines {
		b.WriteString(rToolOutput(line))
		b.WriteByte('\n')
	}
	if hiddenCount > 0 {
		b.WriteString(rMuted(fmt.Sprintf("… %d more lines (", hiddenCount)))
		b.WriteString(rToolOutput(r.KeyExpand))
		b.WriteString(rMuted(" to expand)"))
		b.WriteByte('\n')
	}
	if parsed.footer != "" {
		b.WriteString(rMuted(parsed.footer))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (r *WebFetchRenderer) PreviewLines() int             { return 0 }
func (r *WebFetchRenderer) HideResultWhenCollapsed() bool { return true }

type webFetchParsedOutput struct {
	header string
	body   string
	footer string
}

func (r *WebFetchRenderer) parseOutput(output string) webFetchParsedOutput {
	p := webFetchParsedOutput{}
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return p
	}
	p.header = lines[0]
	if len(lines) == 1 {
		return p
	}
	// Last line is the footer if it starts with "(end".
	last := lines[len(lines)-1]
	if strings.HasPrefix(last, "(end") {
		p.footer = last
		lines = lines[:len(lines)-1]
	}
	p.body = strings.Join(lines[1:], "\n")
	return p
}
