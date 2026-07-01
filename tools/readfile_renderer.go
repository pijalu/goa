// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
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
	// Read file results show ONLY metadata (path, offset).
	// File content is never displayed in the TUI — the agent sees it in
	// its tool result and the user trusts the agent to use it correctly.
	header := parseReadFileHeaderPath(extractReadHeader(output))
	if header == "" {
		if ctx.IsError {
			return rToolOutput(output)
		}
		return ""
	}
	// Metadata is already shown on the call line as `read <path>:<range>`.
	return ""
}

func extractReadHeader(output string) string {
	if idx := strings.IndexByte(output, '\n'); idx > 0 {
		return output[:idx]
	}
	return ""
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

// parseReadFileHeaderPath extracts the path from a "read file <path>:..." header.
func parseReadFileHeaderPath(header string) string {
	const prefix = "read file "
	if !strings.HasPrefix(header, prefix) {
		return ""
	}
	rest := header[len(prefix):]
	if idx := strings.IndexByte(rest, ':'); idx >= 0 {
		return rest[:idx]
	}
	return rest
}

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
