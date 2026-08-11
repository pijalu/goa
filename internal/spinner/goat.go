// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package spinner

// Goat returns the "goat vs. brick wall" waiting animation: a goat charges
// headfirst into a brick wall on the left across a 6-cell line, hits it, the
// ball bounces back off the wall through the dust cloud, and the wall
// rebuilds before the loop restarts:
//
//	🧱⠂⠂⠂⠂🐐   goat lines up at the far right
//	🧱⠂⠂⠂🐐💨   it charges, speed lines behind
//	🧱⠂⠂🐐💨⠂   gaining speed…
//	🧱⠂🐐💨⠂⠂   closing in…
//	🧱🐐💨⠂⠂⠂   impact!
//	💥🔥⠂⠂⠂⠂   headbutt! boom + fire
//	✨💫⠂⠂⠂⠂   sparkle and stars
//	💨🌫️⠂⠂⠂⠂   the dust puffs up
//	🌫️🌫️●⠂⠂⠂   wall of fog — the ball escapes through it
//	🌫️🌫️⠂●⠂⠂   …
//	🌫️🌫️⠂⠂●⠂   …
//	🌫️🌫️⠂⠂⠂●   and rolls out of the frame
//	🧱⠂⠂⠂⠂⠂   the wall rebuilds
//	🧱⠂⠂⠂⠂🐐   the goat is back for another try
//
// The glyphs are colored emoji (and a small filled-circle ball), so no ANSI
// styling is applied (terminal emoji rendering ignores foreground SGR codes).
// Interval 120 ms, like the Pac-Man animation; the loop runs indefinitely
// while the agent processes a request.
func Goat() Definition {
	return Definition{
		Interval: 120,
		Frames: []string{
			"🧱⠂⠂⠂⠂🐐", // goat lines up at the far right
			"🧱⠂⠂⠂🐐💨", // it charges, speed lines behind
			"🧱⠂⠂🐐💨⠂", // gaining speed…
			"🧱⠂🐐💨⠂⠂", // closing in…
			"🧱🐐💨⠂⠂⠂", // impact!
			"💥🔥⠂⠂⠂⠂", // headbutt! boom + fire
			"✨💫⠂⠂⠂⠂", // sparkle and stars
			"💨🌫️⠂⠂⠂⠂", // the dust puffs up
			"🌫️🌫️●⠂⠂⠂", // wall of fog — the ball escapes through it
			"🌫️🌫️⠂●⠂⠂", // …
			"🌫️🌫️⠂⠂●⠂", // …
			"🌫️🌫️⠂⠂⠂●", // and rolls out of the frame
			"🧱⠂⠂⠂⠂⠂", // the wall rebuilds
			"🧱⠂⠂⠂⠂🐐", // the goat is back for another try
		},
	}
}
