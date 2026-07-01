// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// ── Block detection ─────────────────────────────────────────────

func isThematicBreak(s string) bool {
	if len(s) < 3 {
		return false
	}
	c := s[0]
	if c != '-' && c != '*' && c != '_' {
		return false
	}
	count := 0
	for i := 0; i < len(s); i++ {
		if s[i] == c {
			count++
		} else if s[i] != ' ' && s[i] != '\t' {
			return false
		}
	}
	return count >= 3
}

func headingLevel(s string) int {
	level := 0
	for level < len(s) && level < 6 && s[level] == '#' {
		level++
	}
	if level == 0 || level >= len(s) || s[level] != ' ' {
		return 0
	}
	return level
}

func parseHeading(s string) (int, string) {
	lvl := headingLevel(s)
	return lvl, strings.TrimSpace(s[lvl:])
}

func isUnorderedListItem(s string) bool {
	trimmed := strings.TrimLeft(s, " \t")
	if len(trimmed) < 2 {
		return false
	}
	c := trimmed[0]
	if c != '-' && c != '*' && c != '+' {
		return false
	}
	return trimmed[1] == ' ' || trimmed[1] == '\t'
}

func isOrderedListItem(s string) bool {
	trimmed := strings.TrimLeft(s, " \t")
	i := 0
	for i < len(trimmed) && trimmed[i] >= '0' && trimmed[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(trimmed) {
		return false
	}
	return trimmed[i] == '.' && i+1 < len(trimmed) && (trimmed[i+1] == ' ' || trimmed[i+1] == '\t')
}

// ── Block collection ────────────────────────────────────────────

func (r *MDStreamRenderer) collectFencedCode(lines []string, start int) (lang string, content []string, consumed int) {
	open := lines[start]
	lang = strings.TrimSpace(open[3:])
	i := start + 1
	for i < len(lines) {
		if strings.HasPrefix(lines[i], "```") {
			return lang, content, i - start + 1
		}
		content = append(content, lines[i])
		i++
	}
	// Unclosed fence - still render as code block
	return lang, content, i - start
}

func (r *MDStreamRenderer) collectBlockquote(lines []string, start int) (content []string, consumed int) {
	i := start
	for i < len(lines) {
		trimmed := strings.TrimLeft(lines[i], " \t")
		if !strings.HasPrefix(trimmed, ">") {
			// Check if this is a lazy continuation (blank line or indented)
			if trimmed != "" && !strings.HasPrefix(lines[i], " ") {
				break
			}
			if trimmed == "" {
				// Blank line inside blockquote? Include it as empty content
				content = append(content, "")
				i++
				continue
			}
		}
		// Strip the leading > and optional space
		text := strings.TrimLeft(lines[i], " \t")
		if strings.HasPrefix(text, "> ") {
			content = append(content, text[2:])
		} else if strings.HasPrefix(text, ">") {
			content = append(content, text[1:])
		} else {
			content = append(content, text)
		}
		i++
	}
	return content, i - start
}

func (r *MDStreamRenderer) collectParagraph(lines []string, start int) (content []string, consumed int) {
	i := start
	for i < len(lines) {
		if strings.TrimSpace(lines[i]) == "" {
			break
		}
		// Stop at block-level constructs
		if isThematicBreak(strings.TrimSpace(lines[i])) ||
			headingLevel(lines[i]) > 0 ||
			strings.HasPrefix(lines[i], "```") ||
			strings.HasPrefix(lines[i], ">") ||
			isUnorderedListItem(lines[i]) ||
			isOrderedListItem(lines[i]) ||
			isTableRow(lines[i]) ||
			isTableSeparator(lines[i]) {
			break
		}
		content = append(content, lines[i])
		i++
	}
	return content, i - start
}

func (r *MDStreamRenderer) collectUnorderedList(lines []string, start int) (items [][]string, consumed int) {
	return r.collectList(lines, start, isUnorderedListItem)
}

func (r *MDStreamRenderer) collectOrderedList(lines []string, start int) (items [][]string, consumed int) {
	return r.collectList(lines, start, isOrderedListItem)
}

func (r *MDStreamRenderer) collectList(lines []string, start int, isItem func(string) bool) (items [][]string, consumed int) {
	i := start
	var current []string

	for i < len(lines) {
		if shouldEndListAtBlank(lines, i, isItem, current) {
			items = append(items, current)
			return items, i - start
		}

		if strings.TrimSpace(lines[i]) == "" {
			i++
			continue
		}

		if isItem(lines[i]) {
			if len(current) > 0 {
				items = append(items, current)
			}
			current = []string{lines[i]}
			i++
			continue
		}

		// Continuation line
		if len(current) > 0 {
			current = append(current, lines[i])
		}
		i++
	}

	if len(current) > 0 {
		items = append(items, current)
	}
	return items, i - start
}

func shouldEndListAtBlank(lines []string, i int, isItem func(string) bool, current []string) bool {
	if strings.TrimSpace(lines[i]) != "" {
		return false
	}
	if len(current) == 0 {
		return false
	}
	if i+1 >= len(lines) {
		return false
	}
	next := lines[i+1]
	return !isItem(next) && strings.TrimSpace(next) != ""
}

// ── Block rendering ─────────────────────────────────────────────

func (r *MDStreamRenderer) renderHeading(level int, text string) []string {
	prefix := strings.Repeat("#", level) + " "
	rendered := renderInline(text, r.theme)
	color := r.theme.ColorHex("heading_fg")
	if color == "" {
		color = r.theme.ColorHex("user_msg")
	}

	full := ansi.Fg(color) + ansi.Bold + prefix + rendered + ansi.Reset
	wrapped := ansi.Wrap(full, r.width)
	if len(wrapped) > 0 {
		// Only first line gets the prefix style; continuations are indented
		for i := 1; i < len(wrapped); i++ {
			wrapped[i] = strings.Repeat(" ", len(prefix)) + wrapped[i]
		}
	}
	return wrapped
}

func (r *MDStreamRenderer) renderParagraph(lines []string) []string {
	text := strings.Join(lines, " ")
	rendered := renderInline(text, r.theme)
	return ansi.Wrap(rendered, r.width)
}

func (r *MDStreamRenderer) renderFencedCode(lang string, lines []string) []string {
	var result []string
	// Empty line before the code block uses the default background.
	result = append(result, "")

	bg := ansi.Bg(r.theme.ColorHex("code_bg"))
	fg := ansi.Fg(r.theme.ColorHex("code_fg"))
	if bg == ansi.Bg("") || bg == ansi.Bg("#888888") {
		bg = ansi.Bg("#21262d")
	}
	if fg == ansi.Fg("") || fg == ansi.Fg("#888888") {
		fg = ansi.Fg("#8b949e")
	}

	if lang != "" {
		labelText := " " + lang + " "
		labelFG := ansi.Fg("#8b949e") + ansi.Faint
		pad := r.width - ansi.Width(labelText)
		if pad < 0 {
			pad = 0
		}
		result = append(result, bg+labelFG+labelText+strings.Repeat(" ", pad)+ansi.Reset)
	}
	for _, line := range lines {
		line = ansi.ExpandTabs(line, ansi.TabWidth)
		// Apply basic syntax highlighting
		colored := highlightLine(line, lang, fg)
		prefix := "  "
		avail := r.width - ansi.Width(prefix)
		if avail < 8 {
			avail = r.width
			prefix = ""
		}
		if ansi.Width(colored) > avail {
			colored = ansi.Truncate(colored, avail-3) + "…"
		}
		// Extend the background color across the entire line width.
		visible := prefix + colored
		pad := r.width - ansi.Width(visible)
		if pad < 0 {
			pad = 0
		}
		result = append(result, bg+prefix+fg+colored+strings.Repeat(" ", pad)+ansi.Reset)
	}
	// Empty line after the code block uses the default background.
	result = append(result, "")
	return result
}

func (r *MDStreamRenderer) renderBlockquote(lines []string) []string {
	var result []string
	color := ansi.Fg(r.theme.ColorHex("quote_fg"))
	if color == ansi.Fg("") || color == ansi.Fg("#888888") {
		color = ansi.Fg("#8b949e")
	}

	prefix := "│ "
	indent := "│ "
	for _, line := range lines {
		rendered := renderInline(line, r.theme)
		wrapped := ansi.Wrap(rendered, r.width-ansi.Width(indent))
		for i, w := range wrapped {
			if i == 0 {
				result = append(result, color+prefix+w+ansi.Reset)
			} else {
				result = append(result, color+indent+w+ansi.Reset)
			}
		}
	}
	return result
}

func (r *MDStreamRenderer) renderUnorderedList(items [][]string) []string {
	var result []string
	marker := "• "
	indent := "  "
	for _, item := range items {
		text := extractListItemText(item, isUnorderedListItem)
		rendered := renderInline(text, r.theme)
		wrapped := ansi.Wrap(rendered, r.width-ansi.Width(indent))
		for i, w := range wrapped {
			if i == 0 {
				result = append(result, marker+w)
			} else {
				result = append(result, indent+w)
			}
		}
	}
	return result
}

func (r *MDStreamRenderer) renderOrderedList(items [][]string) []string {
	var result []string
	for idx, item := range items {
		marker := fmt.Sprintf("%d. ", idx+1)
		indent := strings.Repeat(" ", len(marker))
		text := extractListItemText(item, isOrderedListItem)
		rendered := renderInline(text, r.theme)
		wrapped := ansi.Wrap(rendered, r.width-ansi.Width(indent))
		for i, w := range wrapped {
			if i == 0 {
				result = append(result, marker+w)
			} else {
				result = append(result, indent+w)
			}
		}
	}
	return result
}

func extractListItemText(item []string, isItem func(string) bool) string {
	if len(item) == 0 {
		return ""
	}
	text := stripListMarker(item[0])
	for j := 1; j < len(item); j++ {
		if !isItem(item[j]) {
			text += " " + strings.TrimSpace(item[j])
		}
	}
	return text
}

func stripListMarker(line string) string {
	trimmed := strings.TrimLeft(line, " \t")
	i := 0
	for i < len(trimmed) && isMarkerChar(trimmed[i]) {
		i++
	}
	for i < len(trimmed) && (trimmed[i] == ' ' || trimmed[i] == '\t') {
		i++
	}
	return trimmed[i:]
}

func isMarkerChar(c byte) bool {
	return c == '-' || c == '*' || c == '+' || c == '.' || (c >= '0' && c <= '9')
}
