// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package ansi

import "strings"

// Box-drawing symbols: the single source of truth for every box-drawing rune
// rendered by the TUI and command output (panel borders, tables, separators).
// Components must reference these named constants instead of embedding raw
// rune literals so a glyph change stays a one-line edit here (goal G3).
const (
	// Lines.
	BoxHorizontal = "─" // U+2500 light horizontal
	BoxVertical   = "│" // U+2502 light vertical

	// Sharp corners.
	BoxTopLeft     = "┌" // U+250C down-and-right
	BoxTopRight    = "┐" // U+2510 down-and-left
	BoxBottomLeft  = "└" // U+2514 up-and-right
	BoxBottomRight = "┘" // U+2518 up-and-left

	// Rounded corners.
	BoxRoundedTopLeft     = "╭" // U+256D arc down-and-right
	BoxRoundedTopRight    = "╮" // U+256E arc down-and-left
	BoxRoundedBottomRight = "╯" // U+256F arc up-and-left
	BoxRoundedBottomLeft  = "╰" // U+2570 arc up-and-right

	// Junctions (three-way) and cross.
	BoxJunctionRight = "├" // U+251C vertical + arm to the right
	BoxJunctionLeft  = "┤" // U+2524 vertical + arm to the left
	BoxJunctionDown  = "┬" // U+252C horizontal + arm downward
	BoxJunctionUp    = "┴" // U+2534 horizontal + arm upward
	BoxCross         = "┼" // U+253C four-way cross

	// Title brackets: heavier verticals flanking an embedded label inside a
	// ruled line (renderTitledBorder's "───<title>───" style).
	BoxTitleLeft  = "┨" // U+2528 vertical heavy, arm left
	BoxTitleRight = "┠" // U+2520 vertical heavy, arm right
)

// RepeatHorizontal returns n BoxHorizontal bars concatenated ("──…").
// Counts <= 0 yield the empty string, matching strings.Repeat semantics
// without panicking on negative input at call sites that compute widths.
func RepeatHorizontal(n int) string {
	if n <= 0 {
		return ""
	}
	return strings.Repeat(BoxHorizontal, n)
}
