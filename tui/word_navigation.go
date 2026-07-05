// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "unicode"

// findWordBackward finds the start of the word preceding cursor.
// Returns the rune position of the word start.
func findWordBackward(text string, cursor int) int {
	if cursor <= 0 {
		return 0
	}

	runes := []rune(text)
	if cursor > len(runes) {
		cursor = len(runes)
	}

	pos := cursor
	// Skip trailing whitespace
	for pos > 0 && unicode.IsSpace(runes[pos-1]) {
		pos--
	}
	if pos == 0 {
		return 0
	}
	// Skip non-whitespace (the word)
	for pos > 0 && !unicode.IsSpace(runes[pos-1]) {
		pos--
	}
	return pos
}

// findWordForward finds the end of the word following cursor.
// Returns the rune position of the word end.
func findWordForward(text string, cursor int) int {
	runes := []rune(text)
	if cursor >= len(runes) {
		return len(runes)
	}

	pos := cursor
	// Skip leading whitespace
	for pos < len(runes) && unicode.IsSpace(runes[pos]) {
		pos++
	}
	if pos >= len(runes) {
		return len(runes)
	}
	// Skip non-whitespace (the word)
	for pos < len(runes) && !unicode.IsSpace(runes[pos]) {
		pos++
	}
	return pos
}

// findLineStart finds the start of the current line.
func findLineStart(text string, cursor int) int {
	if cursor <= 0 {
		return 0
	}
	runes := []rune(text)
	if cursor > len(runes) {
		cursor = len(runes)
	}
	pos := cursor - 1
	for pos >= 0 && runes[pos] != '\n' {
		pos--
	}
	return pos + 1
}

// findLineEnd finds the end of the current line (position of next newline or end).
func findLineEnd(text string, cursor int) int {
	runes := []rune(text)
	if cursor >= len(runes) {
		return len(runes)
	}
	pos := cursor
	for pos < len(runes) && runes[pos] != '\n' {
		pos++
	}
	return pos
}

// cursorLogicalLine returns the 0-indexed logical line number for a cursor position.
func cursorLogicalLine(text string, pos int) int {
	if pos <= 0 {
		return 0
	}
	line := 0
	for i, r := range text {
		if i >= pos {
			break
		}
		if r == '\n' {
			line++
		}
	}
	return line
}

// visualLine maps a visual (wrapped) line to its rune position range.
type visualLine struct {
	logicalLine int // which logical line (0-indexed)
	bufStart    int // rune index in the buffer where this visual line starts
	runeCount   int // number of runes in this visual line
}

// buildVisualLineMap computes the visual line layout for wrapped text at a given width.
func buildVisualLineMap(text string, width int) []visualLine {
	if width <= 0 {
		width = 80
	}
	var result []visualLine
	runes := []rune(text)
	bufPos := 0
	for bufPos < len(runes) {
		// Find end of logical line
		lineEnd := bufPos
		for lineEnd < len(runes) && runes[lineEnd] != '\n' {
			lineEnd++
		}
		lineRunes := runes[bufPos:lineEnd]
		if len(lineRunes) == 0 {
			result = append(result, visualLine{
				logicalLine: countNewlines(string(runes[:bufPos])),
				bufStart:    bufPos,
				runeCount:   0,
			})
		} else {
			// Wrap the logical line into visual lines
			visLines := wrapRunesAtWidth(lineRunes, width)
			for _, vl := range visLines {
				result = append(result, visualLine{
					logicalLine: countNewlines(string(runes[:bufPos])),
					bufStart:    bufPos + vl.bufStart,
					runeCount:   vl.runeCount,
				})
			}
		}
		// Skip past newline
		bufPos = lineEnd + 1
	}
	// If the text ends with a newline, the cursor can sit on a trailing empty
	// logical line. Add a visual line for it so cursor movement works correctly.
	if len(runes) > 0 && runes[len(runes)-1] == '\n' {
		result = append(result, visualLine{
			logicalLine: countNewlines(string(runes)),
			bufStart:    bufPos,
			runeCount:   0,
		})
	}
	return result
}

// wrapRunesAtWidth wraps a sequence of runes (no newlines) to the given width.
// Returns each visual line's offset within the input and rune count.
func wrapRunesAtWidth(runes []rune, width int) []visualLine {
	var result []visualLine
	offset := 0
	for offset < len(runes) {
		lineWidth := 0
		end := offset
		for end < len(runes) {
			rw := ansiWidth(runes[end])
			if lineWidth+rw > width && end > offset {
				break
			}
			lineWidth += rw
			end++
		}
		if end == offset {
			end = offset + 1
		}
		result = append(result, visualLine{
			bufStart:  offset,
			runeCount: end - offset,
		})
		offset = end
	}
	return result
}

// findVisualLine returns the index into the visual line map for the given buffer
// position (rune index). text is the current buffer content. Handles:
//   - Normal positions within a visual line.
//   - Wrapped-line boundaries: the position at the exact end of a wrapped visual
//     line belongs to the NEXT visual line, not the current one.
//   - Logical-line end: the position at a newline or at EOF belongs to the
//     current visual line.
//   - Empty lines (runeCount=0): match if pos is exactly at bufStart.
func findVisualLine(text string, vlm []visualLine, pos int) int {
	runes := []rune(text)
	textLen := len(runes)
	for i, vl := range vlm {
		if pos == vl.bufStart && vl.runeCount == 0 {
			return i
		}
		end := vl.bufStart + vl.runeCount
		if pos >= vl.bufStart && pos < end {
			return i
		}
		if pos == end && (pos == textLen || runes[pos] == '\n') {
			return i
		}
	}
	// If pos is before any line, return first line
	if len(vlm) > 0 && pos < vlm[0].bufStart {
		return 0
	}
	return len(vlm) - 1
}

// countNewlines returns the number of '\n' characters in s.
func countNewlines(s string) int {
	n := 0
	for _, r := range s {
		if r == '\n' {
			n++
		}
	}
	return n
}

// ansiWidth returns the display width of a single rune.
// Handles tabs (width 4) and newlines (0), delegates to runeWidth for others.
func ansiWidth(r rune) int {
	if r == '\t' {
		return 4
	}
	if r == '\n' {
		return 0
	}
	return runeWidth(r)
}
