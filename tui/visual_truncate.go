// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
)

// VisualTruncateResult holds the result of visual line truncation.
type VisualTruncateResult struct {
	VisualLines  []string
	SkippedCount int
}

// TruncateToVisualLines renders text through a temporary Text component and
// counts actual visual lines (accounting for word wrap at given width).
// Returns the last maxVisualLines of visual output.
// Visual truncation of strings to a max width (inspired by pi).
func TruncateToVisualLines(text string, maxVisualLines, width int) VisualTruncateResult {
	if text == "" || width <= 0 {
		return VisualTruncateResult{}
	}

	// Render through a Text component to get visual lines with wrapping
	tempText := NewText(text, 0, 0)
	allVisualLines := tempText.Render(width)

	if len(allVisualLines) <= maxVisualLines {
		return VisualTruncateResult{
			VisualLines:  allVisualLines,
			SkippedCount: 0,
		}
	}

	// Take the last N visual lines
	truncatedLines := allVisualLines[len(allVisualLines)-maxVisualLines:]
	skippedCount := len(allVisualLines) - maxVisualLines

	return VisualTruncateResult{
		VisualLines:  truncatedLines,
		SkippedCount: skippedCount,
	}
}

// CollapseConsecutiveBlanks reduces multiple consecutive blank lines to at most 2.
func CollapseConsecutiveBlanks(lines []string) []string {
	if len(lines) == 0 {
		return lines
	}
	var result []string
	blankCount := 0
	for _, line := range lines {
		if strings.TrimSpace(line) == "" {
			blankCount++
			if blankCount <= 2 {
				result = append(result, line)
			}
		} else {
			blankCount = 0
			result = append(result, line)
		}
	}
	return result
}
