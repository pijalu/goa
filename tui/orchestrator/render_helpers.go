// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

import (
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

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

// truncField trims s to n visible columns, appending an ellipsis when trimmed.
// The width check uses visibleLen (ANSI-aware + rune-aware) so multi-byte or
// ANSI-bearing strings whose byte length exceeds n but whose visible width
// fits are NOT truncated too early. Truncation occurs at a rune boundary and
// preserves ANSI escape sequences, so styles are not split mid-sequence.
func truncField(s string, n int) string {
	if visibleLen(s) <= n {
		return s
	}
	if n <= 1 {
		return "…"
	}
	var b strings.Builder
	w := 0
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			b.WriteRune(r)
			continue
		}
		if inEsc {
			b.WriteRune(r)
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		if w >= n-1 {
			break
		}
		b.WriteRune(r)
		w++
	}
	b.WriteString(ansi.Reset + "…")
	return b.String()
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

// padToWidth right-pads a line (which may contain ANSI) with plain spaces so
// its visible width equals width. Lines already at least as wide are returned
// unchanged. Used so the stats header/footer background spans the full panel
// width instead of leaving a ragged trailing gap.
func padToWidth(line string, width int) string {
	v := visibleLen(line)
	if v >= width {
		return line
	}
	return line + strings.Repeat(" ", width-v)
}

// fit clips then pads a line to exactly width visible columns: it guarantees
// the line is no wider than width AND no narrower, so every row of the stats
// panel has a stable height and the background fills the panel.
func fit(line string, width int) string {
	return padToWidth(clip(line, width), width)
}
