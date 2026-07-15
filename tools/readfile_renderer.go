// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/pijalu/goa/internal/tuirender"
)

// ReadFileRenderer renders read tool calls and results.
// tool renderer.
type ReadFileRenderer struct {
	KeyExpand string
}

// Compile-time interface check.
var _ tuirender.ToolRenderer = (*ReadFileRenderer)(nil)

func NewReadFileRenderer() *ReadFileRenderer {
	return &ReadFileRenderer{KeyExpand: "Ctrl+O"}
}

func (r *ReadFileRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	path := stringArg(args, "path")
	start := intArg(args, "start_line")
	end := intArg(args, "end_line")
	maxLines := intArg(args, "max_lines")

	pathDisplay := path
	if pathDisplay == "" {
		pathDisplay = "..."
	}

	var rangeSuffix string
	if start > 0 || end > 0 || maxLines > 0 {
		startDisplay := 1
		if start > 0 {
			startDisplay = start
		}
		endDisplay := ""
		if end > 0 {
			endDisplay = fmt.Sprintf("-%d", end)
		} else if maxLines > 0 {
			endDisplay = fmt.Sprintf("-%d", startDisplay+maxLines-1)
		}
		rangeSuffix = rMuted(fmt.Sprintf(":%d%s", startDisplay, endDisplay))
	}

	return rToolTitle("read") + " " + rAccent(pathDisplay) + rangeSuffix
}

func (r *ReadFileRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}
	if ctx.IsError {
		return rToolOutput(output)
	}
	if !ctx.Expanded {
		// Summary (collapsed): header only. The path/range already appears on
		// the call line, so there is nothing to add — matching pi, which
		// returns "" unless the block is expanded or the result is an error.
		return ""
	}
	// Full (expanded): render the embedded file content, highlighted, with a
	// "... (N more lines)" hint when the read was truncated.
	content, _, remaining := extractReadContent(output)
	if len(content) == 0 {
		return ""
	}
	path := stringArg(ctx.Args, "path")
	return formatReadContent(content, path, remaining, r.KeyExpand)
}

// readEndRe parses the trailing "(end — N lines shown[, M remaining])" footer
// emitted by the read tool. Capture 1 = shown; optional capture 2 = remaining.
var readEndRe = regexp.MustCompile(`(\d+)\s+lines\s+shown(?:,\s*(\d+)\s+remaining)?`)

// readNumPrefixRe splits a numbered read line into its leading number and the
// code body, e.g. "   123  package foo" → ["   123", "  ", "package foo"].
// The read tool numbers lines as "%6d  %s" when show_numbers is true.
var readNumPrefixRe = regexp.MustCompile(`^(\s*\d+)(\s+)(.*)$`)

// extractReadContent pulls the rendered file content out of a read tool result.
// It skips the "read file <path>:a:b" header, the bracketed [metadata] lines,
// and the trailing "(end — …)" footer, returning the content lines plus the
// remaining-line count (0 when the read was complete or the footer is absent).
func extractReadContent(output string) (lines []string, shown, remaining int) {
	for _, line := range strings.Split(output, "\n") {
		switch {
		case strings.HasPrefix(line, "read file "):
			continue
		case strings.HasPrefix(line, "[") && strings.HasSuffix(line, "]"):
			continue
		case strings.HasPrefix(line, "(end"):
			shown, remaining = parseReadEnd(line)
			return lines, shown, remaining
		default:
			lines = append(lines, line)
		}
	}
	return lines, 0, 0
}

// parseReadEnd extracts shown/remaining counts from a read footer line.
func parseReadEnd(line string) (shown, remaining int) {
	m := readEndRe.FindStringSubmatch(line)
	if len(m) == 0 {
		return 0, 0
	}
	shown, _ = strconv.Atoi(m[1])
	if m[2] != "" {
		remaining, _ = strconv.Atoi(m[2])
	}
	return shown, remaining
}

// formatReadContent renders read content lines for the expanded (Full) view.
// Numbered lines keep their number (muted) with the code body highlighted by
// language; unnumbered/plain lines fall back to the toolOutput color. When the
// read was truncated, a "… N more lines (Ctrl+O to expand)" hint is appended.
func formatReadContent(content []string, path string, remaining int, key string) string {
	lang := getLanguageFromPath(path)
	var b strings.Builder
	for _, line := range content {
		if b.Len() > 0 {
			b.WriteByte('\n')
		}
		b.WriteString(formatReadContentLine(line, lang))
	}
	if remaining > 0 {
		b.WriteByte('\n')
		b.WriteString(expandHint(remaining, key))
	}
	return b.String()
}

// formatReadContentLine renders a single read content line, splitting the
// optional leading line number so only the code body is syntax-highlighted.
func formatReadContentLine(line, lang string) string {
	if lang == "" {
		return rToolOutput(line)
	}
	m := readNumPrefixRe.FindStringSubmatch(line)
	if m == nil {
		return rToolOutput(line)
	}
	return rMuted(m[1]) + m[2] + HighlightLine(m[3], lang)
}

func limitReadLines(lines []string, expanded bool) ([]string, int) {
	maxLines := 10
	if expanded {
		return lines, 0
	}
	if len(lines) <= maxLines {
		return lines, 0
	}
	return lines[:maxLines], len(lines) - maxLines
}

func formatReadLines(lines []string, remaining int, lang, key string) string {
	var b strings.Builder
	for _, line := range lines {
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
		b.WriteString(rToolOutput(key))
		b.WriteString(rMuted(" to expand)"))
	}
	return b.String()
}

func (r *ReadFileRenderer) PreviewLines() int             { return 1 }
func (r *ReadFileRenderer) HideResultWhenCollapsed() bool { return false }

// parseOffset extracts "start:end" from the header "read file <path>:start:end".
func parseOffset(rangeStr string) string {
	const prefix = "read file "
	if !strings.HasPrefix(rangeStr, prefix) {
		return ""
	}
	rest := rangeStr[len(prefix):]
	// Skip past the path (up to first colon)
	if idx := strings.IndexByte(rest, ':'); idx >= 0 {
		offsetStr := rest[idx+1:]
		// Replace colon with dash for display
		offsetStr = strings.ReplaceAll(offsetStr, ":", "-")
		return offsetStr
	}
	return ""
}

// parseTotalLines extracts the line count from the trailing summary line
// like "(end — 316 lines shown)" or "(end — 50 lines shown, 266 remaining)".
func parseTotalLines(output string) string {
	output = strings.TrimSuffix(output, "\n")
	lines := strings.Split(output, "\n")
	if len(lines) == 0 {
		return ""
	}
	last := strings.TrimSpace(lines[len(lines)-1])
	if !strings.HasPrefix(last, "(end") {
		return ""
	}
	return firstNumberAfterMarker(last, "— ")
}

func firstNumberAfterMarker(s, marker string) string {
	idx := strings.Index(s, marker)
	if idx < 0 {
		return ""
	}
	rest := s[idx+len(marker):]
	var num strings.Builder
	for _, r := range rest {
		if r >= '0' && r <= '9' {
			num.WriteRune(r)
		} else if num.Len() > 0 {
			break
		}
	}
	if num.Len() == 0 {
		return ""
	}
	return num.String()
}

func stringArg(args map[string]any, key string) string {
	if v, ok := args[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func intArg(args map[string]any, key string) int {
	if v, ok := args[key]; ok {
		switch n := v.(type) {
		case int:
			return n
		case float64:
			return int(n)
		case string:
			if i, err := strconv.Atoi(n); err == nil {
				return i
			}
		}
	}
	return 0
}
