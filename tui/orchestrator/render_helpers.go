// SPDX-License-Identifier-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

// Shared color palette + text helpers for the multi-agent view renderers
// (stats table, content, tab bar). Kept here so each renderer stays small and
// focused; the old bordered Panel component that previously owned them has been
// removed in favour of the persistent tabbed view.

const (
	colPrimary = "#58a6ff"
	colSuccess = "#3fb950"
	colWarning = "#d29922"
	colDim     = "#8b949e"
	colDanger  = "#f85149"
)

// truncField trims a string to n visible columns, appending an ellipsis.
func truncField(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n-1] + "…"
}

// visibleLen approximates visible length by stripping ANSI escapes.
func visibleLen(s string) int {
	out := 0
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		out++
	}
	return out
}

// clip truncates a line (which may contain ANSI) to width visible columns.
func clip(line string, width int) string {
	if visibleLen(line) <= width {
		return line
	}
	// Best-effort: byte-truncate; rare path (very narrow terminals).
	if len(line) > width {
		return line[:width]
	}
	return line
}
