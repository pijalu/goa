// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package spinner

// Goat returns the "goat headbutt" waiting animation: two goats charge at
// each other from the edges of a 4-cell line, collide with a burst, and leave
// a sparkle before the loop restarts:
//
//	🐐⠂⠂🐐   two goats at the edges
//	⠂🐐⠂🐐   they close in
//	⠂⠂💥⠂   headbutt!
//	⠂✨⠂⠂   sparkle
//
// The glyphs are colored emoji, so no ANSI styling is applied (terminal emoji
// rendering ignores foreground SGR codes). Interval 120 ms, like the Pac-Man
// animation; the loop runs indefinitely while the agent processes a request.
func Goat() Definition {
	return Definition{
		Interval: 120,
		Frames: []string{
			"🐐⠂⠂🐐",
			"⠂🐐⠂🐐",
			"⠂⠂💥⠂",
			"⠂✨⠂⠂",
		},
	}
}
