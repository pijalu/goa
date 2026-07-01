// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

// visibleWidth returns the visible width of a string in terminal columns.
// It strips ANSI escape codes and tabs before measuring.
func visibleWidth(s string) int {
	return ansi.Width(s)
}

// wrapText wraps text to the given width, preserving ANSI codes.
// Splits on newlines and processes each line independently.
func wrapText(text string, width int) []string {
	if text == "" {
		return []string{""}
	}
	if width <= 0 {
		return []string{text}
	}

	var result []string
	for _, line := range strings.Split(text, "\n") {
		wrapped := ansi.Wrap(line, width)
		result = append(result, wrapped...)
	}
	if len(result) == 0 {
		result = []string{""}
	}
	return result
}

// truncateToWidth truncates text to fit within maxWidth visible columns.
// Appends an ellipsis when truncating. Preserves ANSI codes.
func truncateToWidth(text string, maxWidth int, ellipsis string) string {
	if maxWidth <= 0 {
		return ""
	}
	if text == "" {
		return text
	}
	vw := visibleWidth(text)
	if vw <= maxWidth {
		return text
	}
	if ellipsis == "" {
		return ansi.Truncate(text, maxWidth)
	}
	return ansi.Truncate(text, maxWidth-visibleWidth(ellipsis)) + ellipsis
}

// sliceByColumn extracts a range of visual columns from a line.
// Properly handles ANSI codes and wide characters.
// startCol is the starting column (0-indexed), length is the number of columns.
func sliceByColumn(line string, startCol, length int) string {
	if length <= 0 || startCol < 0 {
		return ""
	}
	if startCol == 0 && visibleWidth(line) <= length {
		return line
	}

	endCol := startCol + length
	var result strings.Builder
	col := 0
	for _, r := range line {
		if r == '\x1b' {
			// Pass through ANSI sequences
			result.WriteRune(r)
			continue
		}
		if col >= startCol && col < endCol {
			result.WriteRune(r)
		}
		col++
		if col >= endCol {
			break
		}
	}
	return result.String()
}

// padToWidth pads a line to the given width with spaces on the right.
func padToWidth(line string, width int) string {
	vw := visibleWidth(line)
	if vw >= width {
		return line
	}
	return line + strings.Repeat(" ", width-vw)
}

// padToWidthStyled pads a string to width, wrapping the entire line (content +
// padding) with a background ANSI code so the background color extends across
// the full terminal width. Used by tool execution (§4.1) and bash execution.
//
// The background is applied to the already-padded line instead of prefixing
// the code and adding bare spaces, so the terminal fills the entire line with
// the background color even when differential repainting leaves trailing cells
// untouched.
//
// Any full ANSI reset sequences inside the content are followed by the
// background code so nested styles (e.g. markdown inline code) do not leave
// trailing padding uncolored.
func padToWidthStyled(s string, width int, bgAnsi string) string {
	// Expand tabs to spaces early so terminal tab rendering doesn't break
	// background color continuity (reference implementations do the same).
	s = ansi.ExpandTabs(s, ansi.TabWidth)
	vw := visibleWidth(s)
	var padded string
	if vw >= width {
		padded = s
	} else {
		padded = s + strings.Repeat(" ", width-vw)
	}
	if bgAnsi != "" {
		padded = strings.ReplaceAll(padded, ansi.Reset, ansi.Reset+bgAnsi)
	}
	return bgAnsi + padded + ansi.Reset
}

// runeWidth returns the display width of a single rune.
// East Asian wide characters count as 2.
func runeWidth(r rune) int {
	if r == '\t' {
		return ansi.TabWidth // simplified — caller should track col
	}
	if isWideRune(r) {
		return 2
	}
	return 1
}

// wideRanges lists Unicode ranges where characters have a display width of 2.
// The slice is kept sorted by start code point for binary search.
var wideRanges = []struct{ start, end rune }{
	{0x1100, 0x115F},   // Hangul Jamo
	{0x2600, 0x26FF},   // Miscellaneous Symbols
	{0x2700, 0x27BF},   // Dingbats
	{0x2E80, 0x9FFF},   // CJK Radicals, Ideographs
	{0xAC00, 0xD7AF},   // Hangul Syllables
	{0xF900, 0xFAFF},   // CJK Compatibility Ideographs
	{0xFE30, 0xFE6F},   // CJK Compatibility Forms
	{0xFF01, 0xFF60},   // Fullwidth Forms
	{0xFFE0, 0xFFE6},   // Fullwidth Signs
	{0x1B000, 0x1B0FF}, // Kana Supplement
	{0x1F200, 0x1F2FF}, // Enclosed Ideographic Supplement
	{0x1F300, 0x1F9FF}, // Emoji ranges
	{0x20000, 0x2A6DF}, // CJK Extension B
	{0x2A700, 0x2B73F}, // CJK Extension C
	{0x2B740, 0x2B81F}, // CJK Extension D
	{0x2F800, 0x2FA1F}, // CJK Compatibility Supplement
}

// isWideRune reports whether r belongs to a Unicode range with width 2.
func isWideRune(r rune) bool {
	lo, hi := 0, len(wideRanges)
	for lo < hi {
		mid := (lo + hi) / 2
		if r < wideRanges[mid].start {
			hi = mid
		} else if r > wideRanges[mid].end {
			lo = mid + 1
		} else {
			return true
		}
	}
	return false
}

// dimText returns text wrapped in faint ANSI styling.
func dimText(text string) string {
	return "\x1b[2m" + text + "\x1b[0m"
}
