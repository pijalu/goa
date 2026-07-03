// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/pijalu/goa/internal/tuirender"
)

// SmartSearchRenderer renders smartsearch tool calls and results for the TUI.
type SmartSearchRenderer struct{}

// NewSmartSearchRenderer creates a new SmartSearchRenderer.
func NewSmartSearchRenderer() *SmartSearchRenderer {
	return &SmartSearchRenderer{}
}

// RenderCall renders the smartsearch tool call.
func (r *SmartSearchRenderer) RenderCall(args map[string]any, ctx tuirender.RenderContext) string {
	query, _ := args["query"].(string)
	path, _ := args["path"].(string)
	glob, _ := args["glob"].(string)

	var parts []string
	parts = append(parts, rToolTitle("smartsearch"))

	if query != "" {
		parts = append(parts, rAccent(query))
	}

	if glob != "" {
		parts = append(parts, fmt.Sprintf("in %s", glob))
	}

	if path != "" {
		parts = append(parts, fmt.Sprintf("under %s", shortenHome(path)))
	}

	if mr, ok := args["max_results"].(float64); ok && mr > 0 {
		parts = append(parts, fmt.Sprintf("max:%.0f", mr))
	}

	return strings.Join(parts, " ")
}

// RenderResult renders the smartsearch result output.
func (r *SmartSearchRenderer) RenderResult(output string, ctx tuirender.RenderContext) string {
	if output == "" {
		return ""
	}

	summary := r.parseResult(output)
	if summary != "" {
		return summary
	}

	return rToolOutput(output)
}

// parseResult parses the structured output and returns a formatted summary.
func (r *SmartSearchRenderer) parseResult(output string) string {
	lines := strings.Split(strings.TrimSpace(output), "\n")
	if len(lines) == 0 {
		return ""
	}

	header := lines[0]

	// No results.
	if strings.Contains(header, "No relevant results") || strings.Contains(header, "No files indexed") {
		return r.formatEmpty(header)
	}

	// Parse header for query and count.
	query := r.extractQuery(header)
	count := r.extractTotal(header)

	parts := []string{rToolTitle("smartsearch")}
	if query != "" {
		parts = append(parts, rAccent(query))
	}
	if count != "" {
		parts = append(parts, rMuted(fmt.Sprintf("(%s results)", count)))
	}
	summary := strings.Join(parts, " ")

	// Format file list from remaining lines.
	if len(lines) > 1 {
		fileLines := r.formatFileLines(lines[1:])
		if len(fileLines) > 0 {
			summary += "\n" + strings.Join(fileLines, "\n")
		}
	}

	// Index note at the end.
	for _, line := range lines {
		if strings.Contains(line, "(Index:") {
			summary += "\n" + rMuted(line)
			break
		}
	}

	return summary
}

// formatEmpty renders the "no results" case.
func (r *SmartSearchRenderer) formatEmpty(header string) string {
	query := r.extractQuery(header)
	if query != "" {
		return fmt.Sprintf("%s %s — %s", rToolTitle("smartsearch"), rAccent(query), rMuted("no relevant results"))
	}
	return rToolTitle("smartsearch") + " " + rMuted(header)
}

// formatFileLines formats the numbered result lines and their matching content.
func (r *SmartSearchRenderer) formatFileLines(lines []string) []string {
	// Filter to actual result lines (start with "N. [score] path") and
	// pass through matching content lines (indented, "line: content").
	var resultLines []string
	type lineEntry struct {
		rank    int
		content string
	}

	rankRe := regexp.MustCompile(`^\s*(\d+)\.\s+\[([\d.]+)\]\s+(.+?)(?:\s+\((\d+) lines\))?$`)
	contentRe := regexp.MustCompile(`^(\d+):\s+(.*)$`)
	var entries []lineEntry
	currentRank := 0

	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if m := rankRe.FindStringSubmatch(trimmed); m != nil {
			currentRank, _ = strconv.Atoi(m[1])
			score := m[2]
			filePath := m[3]
			filePath = shortenHome(filePath)
			linesSuffix := ""
			if m[4] != "" {
				linesSuffix = rMuted(fmt.Sprintf(" (%s lines)", m[4]))
			}

			entry := fmt.Sprintf("  %s  %s%s",
				rMuted(fmt.Sprintf("[%s]", score)),
				rAccent(filePath),
				linesSuffix,
			)
			entries = append(entries, lineEntry{rank: currentRank, content: entry})
		} else if cm := contentRe.FindStringSubmatch(trimmed); cm != nil {
			// Matching content line — pass through with formatting.
			lineNum := cm[1]
			content := cm[2]
			if len(content) > 80 {
				content = content[:80] + "…"
			}
			matchEntry := fmt.Sprintf("  %s  %s", rMuted(lineNum), rToolOutput(content))
			entries = append(entries, lineEntry{rank: currentRank, content: matchEntry})
		}
	}

	sort.Slice(entries, func(i, j int) bool {
		return entries[i].rank < entries[j].rank
	})
	for _, e := range entries {
		resultLines = append(resultLines, e.content)
	}
	return resultLines
}

// extractQuery extracts the query from "[smartsearch: "query"]".
func (r *SmartSearchRenderer) extractQuery(header string) string {
	re := regexp.MustCompile(`\[smartsearch:\s*"([^"]+)"\]`)
	m := re.FindStringSubmatch(header)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// extractTotal extracts the hit count from the header.
func (r *SmartSearchRenderer) extractTotal(header string) string {
	re := regexp.MustCompile(`(\d+)\s+results?\b`)
	m := re.FindStringSubmatch(header)
	if len(m) > 1 {
		return m[1]
	}
	return ""
}

// PreviewLines returns the number of preview lines for collapsed display.
func (r *SmartSearchRenderer) PreviewLines() int { return 10 }

// HideResultWhenCollapsed returns false to always show results.
func (r *SmartSearchRenderer) HideResultWhenCollapsed() bool { return false }
