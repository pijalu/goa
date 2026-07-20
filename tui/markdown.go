// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// MDStreamRenderer renders markdown to ANSI incrementally.
// It handles incomplete constructs at EOF by rendering them as plain text.
// When more text arrives and completes the construct, it formats correctly
// on the next render (since the entire stream block is redrawn).
type MDStreamRenderer struct {
	width int
	theme *Theme
}

// NewMDStreamRenderer creates a new markdown renderer.
func NewMDStreamRenderer(width int, theme *Theme) *MDStreamRenderer {
	if width < 10 {
		width = 80
	}
	return &MDStreamRenderer{width: width, theme: theme}
}

// SetWidth updates the terminal width for wrapping.
func (r *MDStreamRenderer) SetWidth(width int) {
	if width < 10 {
		width = 80
	}
	r.width = width
}

// Render converts markdown text to ANSI-formatted lines.
func (r *MDStreamRenderer) Render(text string) []string {
	if text == "" {
		return nil
	}
	lines := strings.Split(text, "\n")
	var result []string
	i := 0

	for i < len(lines) {
		line := lines[i]
		trimmed := strings.TrimSpace(line)

		switch {
		case isThematicBreak(trimmed):
			sep := strings.Repeat("─", r.width)
			result = append(result, ansi.Faint+sep+ansi.Reset)
			i++

		case headingLevel(line) > 0:
			lvl, content := parseHeading(line)
			result = append(result, r.renderHeading(lvl, content)...)
			i++

		case strings.HasPrefix(line, "```"):
			lang, content, consumed := r.collectFencedCode(lines, i)
			result = append(result, r.renderFencedCode(lang, content)...)
			i += consumed

		case strings.HasPrefix(line, ">"):
			content, consumed := r.collectBlockquote(lines, i)
			result = append(result, r.renderBlockquote(content)...)
			i += consumed

		case isUnorderedListItem(line):
			items, consumed := r.collectUnorderedList(lines, i)
			result = append(result, r.renderUnorderedList(items)...)
			i += consumed

		case isOrderedListItem(line):
			items, consumed := r.collectOrderedList(lines, i)
			result = append(result, r.renderOrderedList(items)...)
			i += consumed

		case isTableRow(line):
			header, sep, rows, consumed := r.collectTable(lines, i)
			result = append(result, r.renderTable(header, sep, rows)...)
			i += consumed

		case startsWithBlockGlyph(line):
			var consumed int
			result, consumed = r.renderGraphLines(lines, i, result)
			i += consumed

		case trimmed == "":
			// Blank line - skip but may separate paragraphs
			i++

		default:
			// Paragraph
			content, consumed := r.collectParagraph(lines, i)
			result = append(result, r.renderParagraph(content)...)
			i += consumed
		}
	}

	return result
}

// renderGraphLines renders a run of consecutive graph bar/legend lines
// (e.g. /usage split bars) starting at index start. Each line renders
// standalone — never soft-wrapped into a paragraph — and one empty line is
// appended after the run so the next section stays clear even though source
// blank lines are dropped by the renderer. Returns the updated output and
// the number of source lines consumed.
func (r *MDStreamRenderer) renderGraphLines(lines []string, start int, result []string) ([]string, int) {
	i := start
	for i < len(lines) && startsWithBlockGlyph(lines[i]) {
		result = append(result, ansi.Wrap(renderInline(lines[i], r.theme), r.width)...)
		i++
	}
	return append(result, ""), i - start
}
