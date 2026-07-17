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

// writeFilePreviewLines is the number of content lines shown for a write
// preview while streaming and when the tool block is collapsed. It keeps the
// TUI compact even when writing large files; expanding reveals the full
// content.
const writeFilePreviewLines = 5

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
	return rToolTitle("write") + " " + rAccent(pathDisplay)
}

func (r *WriteFileRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	content := r.resolveContent(output, ctx)
	if content == "" {
		// No fenced content. When the raw output is non-empty (e.g. the
		// "(interrupted)" sentinel set on cancellation, or an error message
		// that is not a code block), surface it verbatim so the body is not
		// silently empty. An empty output (mid-stream, before any result)
		// correctly stays empty.
		if trimmed := strings.TrimSpace(output); trimmed != "" {
			return rToolOutput(trimmed)
		}
		return ""
	}
	path := r.resolvePath(output, ctx)
	return r.renderContent(content, path, ctx)
}

// RenderPartial implements tuirender.StreamingRenderer. While the write tool
// arguments are still streaming, the body shows the accumulated content as a
// compact preview so the user sees the file being written as it arrives.
func (r *WriteFileRenderer) RenderPartial(args map[string]any, ctx tuirender.RenderContext) string {
	ctx.Args = args
	return r.RenderResult("", ctx)
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
// and truncation. Only the displayed lines are highlighted so large writes
// do not waste CPU/memory coloring content that will never be visible.
func (r *WriteFileRenderer) renderContent(content, path string, ctx tuirender.RenderContext) string {
	lang := getLanguageFromPath(path)

	// Expanded view needs every line; build the full slice once. The full
	// content is on screen, so no stats footer is needed.
	if ctx.Expanded {
		allLines := trimTrailingEmptyLines(strings.Split(content, "\n"))
		return r.highlightLines(allLines, lang)
	}

	maxLines := previewLinesFromCtx(ctx, r.PreviewLines())

	// Collapsed/preview path: split only the head needed for display instead
	// of materializing the whole (possibly very large) content per call —
	// important while streaming, when this runs on every args delta.
	displayLines := trimTrailingEmptyLines(splitFirstLines(content, maxLines))
	// Count total lines in a single linear scan (no []string materialization)
	// and drop trailing empty lines to match the trimmed expanded total.
	total := strings.Count(content, "\n") + 1
	total -= countTrailingEmptyLines(content)
	hidden := 0
	if total > len(displayLines) {
		hidden = total - len(displayLines)
	}
	body := r.highlightLines(displayLines, lang)
	return appendWriteStats(body, total, hidden, ctx.IsPartial, r.KeyExpand)
}

// highlightLines renders displayLines with optional syntax highlighting.
// The collapsed-view stats footer is appended separately by renderContent
// (appendWriteStats), so this only produces the content lines themselves.
func (r *WriteFileRenderer) highlightLines(displayLines []string, lang string) string {
	var b strings.Builder
	for _, line := range displayLines {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		if lang == "" {
			b.WriteString(rToolOutput(line))
		} else {
			b.WriteString(HighlightLine(line, lang))
		}
	}
	return b.String()
}

// countTrailingEmptyLines returns how many lines at the end of s are empty,
// matching trimTrailingEmptyLines semantics without splitting the string.
func countTrailingEmptyLines(s string) int {
	n := 0
	for len(s) > 0 {
		i := strings.LastIndexByte(s, '\n')
		line := s[i+1:]
		if line != "" {
			break
		}
		n++
		if i < 0 {
			break
		}
		s = s[:i]
	}
	return n
}

// splitFirstLines returns at most n leading lines of s (split on '\n'),
// stopping early once n lines are collected so it never scans the whole
// string for a small preview.
func splitFirstLines(s string, n int) []string {
	lines := make([]string, 0, n)
	for len(lines) < n {
		i := strings.IndexByte(s, '\n')
		if i < 0 {
			lines = append(lines, s)
			break
		}
		lines = append(lines, s[:i])
		s = s[i+1:]
	}
	return lines
}

func (r *WriteFileRenderer) PreviewLines() int             { return writeFilePreviewLines }
func (r *WriteFileRenderer) HideResultWhenCollapsed() bool { return false }

// appendWriteStats appends the collapsed write footer to body: the total
// number of lines being written, how many are hidden by the preview window
// (when any), and the Ctrl+O affordance to expand the full content. While
// the write is still streaming (partial) the stat is phrased as an ongoing
// "writing N lines"; once the result is in it reports the final "N lines".
// This footer is what carries the write-preparation stats next to the
// widget's own elapsed timer, which lives on the duration line below the
// body. The expanded view shows the full content and omits the footer.
func appendWriteStats(body string, total, hidden int, partial bool, key string) string {
	var b strings.Builder
	b.WriteString(body)
	if body != "" {
		b.WriteByte('\n')
	}
	stat := fmt.Sprintf("%d %s", total, pluralize("line", total))
	if partial {
		stat = "writing " + stat
	}
	b.WriteString(rMuted("… " + stat))
	if hidden > 0 {
		b.WriteString(rMuted(fmt.Sprintf(" (%d more lines, ", hidden)))
	} else {
		b.WriteString(rMuted(" ("))
	}
	b.WriteString(rToolOutput(key))
	b.WriteString(rMuted(" to expand)"))
	return b.String()
}

// pluralize returns the singular or plural form of word based on n.
func pluralize(word string, n int) string {
	if n == 1 {
		return word
	}
	return word + "s"
}

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
