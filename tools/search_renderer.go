// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

// SearchRenderer renders search tool calls and results for the TUI.
type SearchRenderer struct{}

// NewSearchRenderer creates a new SearchRenderer.
func NewSearchRenderer() *SearchRenderer {
	return &SearchRenderer{}
}

// RenderCall renders the search tool call in a human-readable format.
func (r *SearchRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	pattern, _ := args["pattern"].(string)
	path, _ := args["path"].(string)
	glob, _ := args["glob"].(string)
	excludeGlob, _ := args["exclude_glob"].(string)
	caseSensitive, _ := args["case_sensitive"].(bool)
	recursive, _ := args["recursive"].(bool)
	maxResults, _ := args["max_results"].(float64)

	var parts []string
	parts = append(parts, rToolTitle("search"))

	if pattern != "" {
		parts = append(parts, rAccent(pattern))
	}

	if glob != "" {
		parts = append(parts, fmt.Sprintf("in %s", glob))
	}

	if excludeGlob != "" {
		parts = append(parts, fmt.Sprintf("not %s", excludeGlob))
	}

	if path != "" && path != "." {
		parts = append(parts, fmt.Sprintf("under %s", shortenHome(path)))
	}

	if !recursive {
		parts = append(parts, "(non-recursive)")
	}

	if caseSensitive {
		parts = append(parts, "(case-sensitive)")
	}

	if maxResults > 0 {
		parts = append(parts, fmt.Sprintf("max:%.0f", maxResults))
	}

	return strings.Join(parts, " ")
}

// RenderResult renders the search result output.
func (r *SearchRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}

	// Parse the search result to extract summary info
	summary := r.parseSearchResult(output)
	if summary != "" {
		return summary
	}

	// Fallback to raw output
	return rToolOutput(output)
}

// findPatternInHeader extracts the search pattern from a [search: ...] header.
func findPatternInHeader(header string) string {
	patternRe := regexp.MustCompile(`\[search:\s*"?([^"]+)"?\]`)
	m := patternRe.FindStringSubmatch(header)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// parseSearchResult parses the search result and returns a formatted summary
// with files grouped, match counts, and each match on its own line.
func (r *SearchRenderer) parseSearchResult(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return ""
	}
	header := lines[0]

	if strings.Contains(header, "no matching files found") {
		return rToolTitle("search") + " " + rMuted(fmt.Sprintf("for %s: no files found", findPatternInHeader(header)))
	}

	summary := r.formatSearchSummary(header)
	resultLines := r.formatSearchFileLines(lines[1:])

	if len(resultLines) > 0 {
		summary += "\n" + strings.Join(resultLines, "\n")
	}
	return summary
}

// formatSearchSummary builds the summary line for a search result header.
func (r *SearchRenderer) formatSearchSummary(header string) string {
	var totalMatches, truncated string
	if m := regexp.MustCompile(`(\d+) matches across`).FindStringSubmatch(header); m != nil {
		totalMatches = m[1]
	}
	if m := regexp.MustCompile(`(\d+) truncated`).FindStringSubmatch(header); m != nil {
		truncated = m[1]
	}
	pattern := findPatternInHeader(header)

	switch {
	case truncated != "":
		return fmt.Sprintf("%s %s (%s matches, %s truncated)", rToolTitle("search"), rAccent(pattern), totalMatches, rMuted(truncated))
	case totalMatches != "":
		return fmt.Sprintf("%s %s (%s matches)", rToolTitle("search"), rAccent(pattern), totalMatches)
	default:
		return rToolTitle("search") + " " + rMuted(header)
	}
}

// formatSearchFileLines formats the file/match lines from a search result body.
func (r *SearchRenderer) formatSearchFileLines(lines []string) []string {
	var resultLines []string
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !strings.HasPrefix(line, "  ") {
			// File header: "path/file.go: N matches"
			resultLines = append(resultLines, r.formatFileHeader(trimmed))
		} else if strings.Contains(trimmed, ": ") {
			// Content line: "line: content"
			resultLines = append(resultLines, r.formatContentLine(trimmed))
		} else {
			// Line-number line: "15/27/42 (+3 more)"
			resultLines = append(resultLines, fmt.Sprintf("  %s", rMuted(trimmed)))
		}
	}
	return resultLines
}

// formatFileHeader formats a "path: N matches" line.
func (r *SearchRenderer) formatFileHeader(line string) string {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) < 2 {
		return ""
	}
	filePath := shortenHome(strings.TrimSpace(parts[0]))
	suffix := strings.TrimSpace(parts[1])
	return fmt.Sprintf("%s  %s", rAccent(filePath), rMuted(suffix))
}

// formatContentLine formats a "line: content" match line.
func (r *SearchRenderer) formatContentLine(line string) string {
	parts := strings.SplitN(line, ":", 2)
	if len(parts) < 2 {
		return ""
	}
	lineNum := strings.TrimSpace(parts[0])
	// Sanitize defensively (the tool already sanitizes, but renderers must
	// never forward raw ESC bytes to the terminal) and truncate by display
	// width — a byte cut can split a multi-byte rune and render as '�'.
	content := ansi.Sanitize(strings.TrimSpace(parts[1]))
	if ansi.Width(content) > 80 {
		content = ansi.Truncate(content, 80) + "…"
	}
	return fmt.Sprintf("  %s  %s", rMuted(lineNum), rToolOutput(content))
}

// PreviewLines returns the number of preview lines for the search result.
func (r *SearchRenderer) PreviewLines() int { return 20 }

// HideResultWhenCollapsed returns false to show the result when collapsed.
func (r *SearchRenderer) HideResultWhenCollapsed() bool { return false }
