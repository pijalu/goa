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

const (
	ansiColorPrimary = "#58a6ff"
	ansiColorSuccess = "#3fb950"
	ansiColorWarning = "#d29922"
	ansiColorDim     = "#8b949e"
)
