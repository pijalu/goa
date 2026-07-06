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
