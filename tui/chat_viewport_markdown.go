// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "strings"

// isPreformatted detects if text is pre-formatted (aligned columns, indented
// commands, wide lines) and should be rendered line-by-line without markdown
// parsing. Uses three signals:
//  1. Markdown syntax present → NOT preformatted (render as markdown)
//  2. Indented command prefixes ("  /cmd") → preformatted
//  3. Lines wider than 60 chars → preformatted (tabular/error output)
func isPreformatted(text string) bool {
	if looksLikeMarkdown(text) {
		return false // has markdown syntax — needs the renderer
	}
	lines := strings.Split(text, "\n")
	if len(lines) < 2 {
		return false // single line is never preformatted
	}
	hasIndentedCommands := false
	longLines := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		// Pattern: indented commands like "  /command"
		if strings.HasPrefix(line, "  /") {
			hasIndentedCommands = true
		}
		// Pattern: wide lines (tabular, JSON, stack traces)
		if len([]rune(line)) > 60 {
			longLines++
		}
	}
	return hasIndentedCommands || longLines >= 2
}

// looksLikeMarkdown returns true if text contains markdown formatting patterns.
func looksLikeMarkdown(text string) bool {
	lines := strings.Split(text, "\n")
	if len(lines) < 3 {
		return false
	}
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if hasMDHeader(trimmed) || hasMDFence(trimmed) || hasMDListItem(trimmed) ||
			hasMDTable(trimmed) || hasMDThematicBreak(trimmed) || hasMDInlineCode(trimmed) {
			return true
		}
	}
	return false
}

func hasMDHeader(trimmed string) bool {
	return len(trimmed) > 1 && trimmed[0] == '#' && trimmed[1] == ' '
}

func hasMDFence(trimmed string) bool {
	return strings.HasPrefix(trimmed, "```")
}

func hasMDListItem(trimmed string) bool {
	// Bullet lists: - item, * item
	if len(trimmed) > 1 && (trimmed[0] == '-' || trimmed[0] == '*') && trimmed[1] == ' ' {
		return true
	}
	// Numbered lists: 1. item
	if len(trimmed) > 1 && trimmed[0] >= '0' && trimmed[0] <= '9' && trimmed[1] == '.' {
		return true
	}
	return false
}

func hasMDTable(trimmed string) bool {
	return strings.HasPrefix(trimmed, "|") && strings.HasSuffix(trimmed, "|")
}

func hasMDThematicBreak(trimmed string) bool {
	if strings.Count(trimmed, "-") >= 3 && len(trimmed) <= 5 && strings.TrimLeft(trimmed, "-") == "" {
		return true
	}
	if strings.Count(trimmed, "*") >= 3 && len(trimmed) <= 5 && strings.TrimLeft(trimmed, "*") == "" {
		return true
	}
	return false
}

func hasMDInlineCode(trimmed string) bool {
	return strings.Contains(trimmed, "`")
}
