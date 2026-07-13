// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/tuirender"
)

// WriteFileRenderer renders write tool calls and results.
// tool renderer.
type WriteFileRenderer struct {
	KeyExpand string
}

// writeFilePreviewLines is the default number of content lines shown for a
// write result. Large enough to display typical writes without truncation.
const writeFilePreviewLines = 1000

var _ tuirender.ToolRenderer = (*WriteFileRenderer)(nil)

func NewWriteFileRenderer() *WriteFileRenderer {
	return &WriteFileRenderer{KeyExpand: "Ctrl+O"}
}

func (r *WriteFileRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	path := stringArg(args, "path")
	pathDisplay := shortenHome(path)
	if pathDisplay == "" {
		pathDisplay = "..."
	}

	// During streaming (ArgsComplete == false), keep the header minimal: the
	// body will render the streaming content as it arrives.
	if !ctx.ArgsComplete {
		return rToolTitle("write") + " " + rAccent(pathDisplay) + rMuted(" ...")
	}
	return rToolTitle("write") + " " + rAccent(pathDisplay)
}

func (r *WriteFileRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	content := r.resolveContent(output, ctx)
	if content == "" {
		return ""
	}
	path := r.resolvePath(output, ctx)
	return r.renderContent(content, path, ctx)
}

// resolveContent returns the content to display, preferring the final tool
// output and falling back to streamed partial args while the call is still
// in progress.
func (r *WriteFileRenderer) resolveContent(output string, ctx tuirender.RenderContext) string {
	content := extractWriteContent(output)
	if content == "" && ctx.IsPartial {
		if partial, ok := ctx.Args["content"].(string); ok && partial != "" {
			content = partial
		}
	}
	return content
}

// resolvePath returns the file path from the tool output or the parsed args.
func (r *WriteFileRenderer) resolvePath(output string, ctx tuirender.RenderContext) string {
	path := stringArg(parseResultHeader(output), "path")
	if path == "" {
		path = stringArg(ctx.Args, "path")
	}
	return path
}

// renderContent formats the content lines with optional syntax highlighting
// and truncation.
func (r *WriteFileRenderer) renderContent(content, path string, ctx tuirender.RenderContext) string {
	lang := getLanguageFromPath(path)

	lines := strings.Split(content, "\n")
	if lang != "" {
		lines = HighlightCode(content, lang)
	}
	lines = trimTrailingEmptyLines(lines)

	maxLines := r.PreviewLines()
	if ctx.Expanded {
		maxLines = len(lines)
	}
	displayLines := lines
	if len(lines) > maxLines {
		displayLines = lines[:maxLines]
	}
	remaining := len(lines) - maxLines

	var b strings.Builder
	for _, line := range displayLines {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		if lang == "" {
			b.WriteString(rToolOutput(line))
		} else {
			b.WriteString(line)
		}
	}
	if remaining > 0 {
		b.WriteString("\n")
		b.WriteString(rMuted(fmt.Sprintf("... (%d more lines, ", remaining)))
		b.WriteString(rToolOutput(r.KeyExpand))
		b.WriteString(rMuted(" to expand)"))
	}
	return b.String()
}

func (r *WriteFileRenderer) PreviewLines() int             { return writeFilePreviewLines }
func (r *WriteFileRenderer) HideResultWhenCollapsed() bool { return false }

// extractWriteContent pulls the fenced code block out of write output.
func extractWriteContent(output string) string {
	start := strings.Index(output, "```\n")
	if start < 0 {
		return ""
	}
	start += 4
	end := strings.Index(output[start:], "\n```")
	if end < 0 {
		return output[start:]
	}
	return output[start : start+end]
}

// parseResultHeader extracts a map of key/value pairs from the first line of
// write output, e.g. "[write: main.go]".
func parseResultHeader(output string) map[string]any {
	m := make(map[string]any)
	if idx := strings.IndexByte(output, '\n'); idx > 0 {
		line := output[:idx]
		line = strings.Trim(line, "[]")
		parts := strings.SplitN(line, ":", 2)
		if len(parts) == 2 {
			m["path"] = strings.TrimSpace(parts[1])
		}
	}
	return m
}
