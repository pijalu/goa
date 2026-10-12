// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

// Palette256 maps an xterm 256-colour index to a CSS colour. The engine
// emits truecolor, but a palette spec (or a tool echoing third-party bytes)
// must still render with the right hue instead of falling back to the theme
// default.
//
// Layout: 0–15 the standard ANSI colours, 16–231 a 6×6×6 colour cube
// (levels 0, 95, 135, 175, 215, 255), 232–255 a 24-step grey ramp
// (8 + 10·n).
func Palette256(n int) string {
	switch {
	case n < 0 || n > 255:
		return ""
	case n < 16:
		return ansi16[n]
	case n < 232:
		i := n - 16
		return rgbHex(cubeLevel(i/36), cubeLevel((i/6)%6), cubeLevel(i%6))
	default:
		v := 8 + 10*(n-232)
		return rgbHex(v, v, v)
	}
}

// ansi16 is the standard xterm base palette, as browser colours. The browser
// renders with the page's own foreground/background for the default (unset)
// colour, so these are only used when a cell explicitly asks for index 0–15.
var ansi16 = [16]string{
	"#000000", "#cd3131", "#0dbc79", "#e5e510",
	"#2472c8", "#bc3fbc", "#11a8cd", "#e5e5e5",
	"#666666", "#f14c4c", "#23d18b", "#f5f543",
	"#3b8eea", "#d670d6", "#29b8db", "#ffffff",
}

func cubeLevel(i int) int {
	levels := [6]int{0, 95, 135, 175, 215, 255}
	if i < 0 || i > 5 {
		return 0
	}
	return levels[i]
}
