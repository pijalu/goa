// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"

	"github.com/pijalu/goa/internal/ansi"
)

func padToWidth(s string, width int) string {
	if width <= 0 {
		return s
	}
	stripped := ansi.Strip(s)
	if len(stripped) >= width {
		return s[:width]
	}
	return s + strings.Repeat(" ", width-len(stripped))
}

// wrapParagraphs wraps free-form text (which may contain embedded newlines —
// goal objectives, reasons and expectations are model-authored) into visual
// rows of at most width columns. ansi.Wrap's contract is single-paragraph, so
// paragraphs are split first and wrapped independently; blank paragraphs keep
// their empty row. Every returned row is guaranteed newline-free: a surviving
// "\n" corrupts the TUI row model because raw-mode LF moves down without a
// carriage return (Goal completion screen corruption:/ "Corruption
// on goal change").
func wrapParagraphs(text string, width int) []string {
	if width <= 0 {
		width = 1
	}
	var out []string
	for _, para := range strings.Split(text, "\n") {
		out = append(out, ansi.Wrap(para, width)...)
	}
	if len(out) == 0 {
		return []string{""}
	}
	return out
}

const (
	ansiColorPrimary = "#58a6ff"
	ansiColorSuccess = "#3fb950"
	ansiColorWarning = "#d29922"
	ansiColorDim     = "#8b949e"
)
