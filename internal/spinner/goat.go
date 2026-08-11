// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package spinner

// Goat returns the "goat vs. brick wall" waiting animation: a goat charges
// headfirst into a brick wall on the left, hits it, and the dust settles
// before the loop restarts:
//
//	🧱⠂⠂🐐   goat lines up at the far right
//	🧱⠂🐐💨   it charges, speed lines behind
//	🧱🐐💨⠂   closing in…
//	💥🔥⠂⠂   headbutt! boom + fire
//	✨💫⠂⠂   sparkle and stars
//	💨🌫️⠂⠂   the dust begins to clear
//	🌫️🌫️⠂⠂   a wall of fog
//	⬜⠂⠂⠂   the wall shows a crack
//	🧱⠂⠂⠂   and rebuilds
//	🧱⠂⠂🐐   the goat is back for another try
//
// The glyphs are colored emoji, so no ANSI styling is applied (terminal emoji
// rendering ignores foreground SGR codes). Interval 120 ms, like the Pac-Man
// animation; the loop runs indefinitely while the agent processes a request.
func Goat() Definition {
	return Definition{
		Interval: 120,
		Frames: []string{
			"🧱⠂⠂🐐", // goat lines up at the far right
			"🧱⠂🐐💨", // it charges, speed lines behind
			"🧱🐐💨⠂", // closing in…
			"💥🔥⠂⠂", // headbutt! boom + fire
			"✨💫⠂⠂", // sparkle and stars
			"💨🌫️⠂⠂", // the dust begins to clear
			"🌫️🌫️⠂⠂", // a wall of fog
			"⬜⠂⠂⠂", // the wall shows a crack
			"🧱⠂⠂⠂", // and rebuilds
			"🧱⠂⠂🐐", // the goat is back for another try
		},
	}
}
