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

// BashRenderer renders bash tool calls and results.
// Approach: raw output, no [bash:] / Exit: metadata wrapping.
type BashRenderer struct {
	KeyExpand string
}

var _ tuirender.ToolRenderer = (*BashRenderer)(nil)

func NewBashRenderer() *BashRenderer {
	return &BashRenderer{KeyExpand: "Ctrl+O"}
}

var (
	bashDurationLineRe = regexp.MustCompile(`^Duration:\s*([\d.]+)s`)
	bashTruncLineRe    = regexp.MustCompile(`^(?:Output truncated|Full output saved to):\s*(.+)$`)
)

func (r *BashRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	cmd := stringArg(args, "command")
	if cmd == "" {
		cmd = "..."
	} else {
		// Collapse whitespace and newlines to a single line so multi-line
		// commands (e.g. python3 -c "...") do not break the tool box layout.
		cmd = strings.Join(strings.Fields(cmd), " ")
		if len(cmd) > 120 {
			cmd = cmd[:117] + "..."
		}
	}
	timeout := intArg(args, "timeout")
	var suffix string
	if timeout > 0 {
		suffix = rMuted(fmt.Sprintf(" (timeout %ds)", timeout))
	}
	return rBashPrompt("$ ") + rToolTitle(cmd) + suffix
}

func (r *BashRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}
	parsed := r.parseOutput(output)

	var b strings.Builder
	if parsed.output != "" {
		lines := strings.Split(parsed.output, "\n")
		maxLines := 5
		if ctx.Expanded {
			maxLines = len(lines)
		}
		displayLines := lines
		if len(lines) > maxLines {
			displayLines = lines[len(lines)-maxLines:]
		}
		hiddenCount := len(lines) - maxLines

		if hiddenCount > 0 {
			b.WriteString(rMuted(fmt.Sprintf("… %d earlier lines (", hiddenCount)))
			b.WriteString(rToolOutput(r.KeyExpand))
			b.WriteString(rMuted(" to expand)"))
			b.WriteByte('\n')
		}
		for _, line := range displayLines {
			b.WriteString(rToolOutput(line))
			b.WriteByte('\n')
		}
	}

	if parsed.truncated && parsed.fullPath != "" {
		b.WriteString(rWarning(fmt.Sprintf("[Full output: %s]", parsed.fullPath)))
		b.WriteByte('\n')
	}

	if parsed.durationMs > 0 {
		label := "Took"
		if ctx.IsPartial {
			label = "Elapsed"
		}
		b.WriteString(rMuted(fmt.Sprintf("%s %.1fs", label, parsed.durationMs/1000.0)))
	}

	result := strings.TrimRight(b.String(), "\n")
	return result
}

func (r *BashRenderer) PreviewLines() int             { return 5 }
func (r *BashRenderer) HideResultWhenCollapsed() bool { return false }

// DefaultBackground returns false so the bash output uses status-based
// background colors (green on success, red on error, amber while running)
// matching other tools in the TUI.
func (r *BashRenderer) DefaultBackground() bool { return false }

type bashParsedOutput struct {
	durationMs float64
	truncated  bool
	fullPath   string
	output     string
}

func (r *BashRenderer) parseOutput(output string) bashParsedOutput {
	p := bashParsedOutput{}
	lines := strings.Split(output, "\n")
	var outputLines []string
	for _, line := range lines {
		if ms, ok := parseBashDurationLine(line); ok {
			p.durationMs = ms
			continue
		}
		if path, ok := parseBashTruncationLine(line); ok {
			p.truncated = true
			p.fullPath = path
			continue
		}
		outputLines = append(outputLines, line)
	}
	p.output = strings.TrimRight(strings.Join(outputLines, "\n"), "\n")
	return p
}

func parseBashDurationLine(line string) (float64, bool) {
	matches := bashDurationLineRe.FindStringSubmatch(line)
	if len(matches) != 2 {
		return 0, false
	}
	f, err := strconv.ParseFloat(matches[1], 64)
	return f * 1000, err == nil
}

func parseBashTruncationLine(line string) (string, bool) {
	matches := bashTruncLineRe.FindStringSubmatch(line)
	if len(matches) != 2 {
		return "", false
	}
	return strings.TrimSpace(matches[1]), true
}
