// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package spinner

import "strings"

// Defender returns the "Defender" story spinner: a tiny evolving terminal
// game rather than a traditional spinner. A hero starts near the right edge
// of a 13-cell line and enemy objects stream in from the left, moving one
// position right every frame until they collide with the hero. Every enemy is
// killed with SMOKE (✨ 💨 🌫️ — no fire), and after each kill the hero moves
// ONE position left, slowly advancing across the line:
//
//	⠂⠂⠂⠂⠂⠂⠂⠂⠂⠂⠂🐐⠂   hero lines up near the right edge
//	🐛⠂⠂⠂⠂⠂⠂⠂⠂⠂⠂🐐⠂   enemy enters from the left…
//	⠂⠂⠂⠂⠂⠂⠂⠂⠂⠂⠂💨⠂   …killed with smoke, hero moves one left
//	⠂🐍⠂⠂⠂⠂⠂⠂⠂⠂🐐⠂⠂   next enemy, hero one place further left
//
// After every 3rd kill the moon 🌙 comes up on the left horizon, arcs across
// the sky, and goes under on the right. From time to time the action pauses
// for a suspense break — a quiet drum-roll of dots accumulating on the left
// (before the 4th enemy and before the final boss) — then the next actor
// strikes. After all eight enemy types have been used once, a final boss
// (☄️ comet or 💣 bomb) reaches the hero. Only a BOMB kills with fire
// (🔥 ☁️ ☢️ 🌫️); a comet kills with smoke (☁️ 🌫️). Either way the hero is
// destroyed and the next hero resets to the right.
//
//	heroes:  🐐 goat → 🦭 seal → 🐊 crocodile → 🦘 kangaroo → 🧑 human → 🐔 chicken → 🐢 turtle
//	enemies: 🐛 🐍 🪨 🧱 🐟 🕷️ 🦂 👾  (shuffled per cycle, no repeat until all used)
//
// The turtle 🐢 is slow: during its cycle every enemy advances at half speed
// (each position is held for two frames), so it takes more time before the
// turtle reaches its enemy. No hero repeats until all seven have been used; the animation loops forever at
// 120 ms per frame. ANSI colors (color terminals render the emoji natively,
// monochrome terminals pick the codes up): yellow sparks, gray smoke/dust,
// red fire (bomb deaths only), green heroes.
const (
	defWidth     = 13 // animation area: 13 cells wide (a longer defender path)
	defStartPos  = 11 // hero starts near the right edge
	defMoonEvery = 3  // moon comes up and goes under after every 3rd kill

	ansiRed    = "\x1b[31m"
	ansiYellow = "\x1b[33m"
	ansiGray   = "\x1b[90m" // bright black renders as gray
	ansiGreen  = "\x1b[32m"
)

// defenderCell is one dynamic cell of a frame: a glyph plus an optional ANSI
// foreground color ("" = native emoji color).
type defenderCell struct {
	glyph string
	color string
}

// Defender builds the complete looping story as one flat frame list.
func Defender() Definition {
	heroes := []string{"🐐", "🦭", "🐊", "🦘", "🧑", "🐔", "🐢"}
	enemies := []string{"🐛", "🐍", "🪨", "🧱", "🐟", "🕷️", "🦂", "👾"}
	bosses := []string{"☄️", "💣"}

	var frames []string
	for cycle, hero := range heroes {
		pos := defStartPos
		// The turtle is slow: its enemies take twice as long to arrive.
		slow := hero == "🐢"
		// Fresh hero steps up at the right edge.
		frames = append(frames, heroAt(hero, pos))

		// Each enemy type enters exactly once per cycle, in a deterministic
		// pseudo-random order (stable across runs, still varied).
		for i, enemy := range deterministicShuffle(enemies, uint64(cycle)) {
			if i == 3 {
				// Suspense break: a quiet drum-roll before the 4th enemy.
				frames = append(frames, suspenseFrames()...)
			}
			frames = append(frames, approachFrames(hero, pos, enemy, slow)...)
			frames = append(frames, smokeKillFrames(pos)...) // ✨ 💨 🌫️ — no fire
			pos--                                           // the hero advances one place left
			if (i+1)%defMoonEvery == 0 {
				frames = append(frames, moonFrames()...) // moon rises and sets
			}
		}

		// Suspense break before the final boss.
		frames = append(frames, suspenseFrames()...)

		// Final boss: comet or bomb (deterministic per cycle). It reaches the
		// hero at its leftmost position; only the BOMB kills with fire. The
		// hero is destroyed either way and the next hero resets to the right.
		boss := bosses[int(lcg(uint64(cycle)+0xB055)%uint64(len(bosses)))]
		frames = append(frames, approachFrames(hero, pos, boss, slow)...)
		frames = append(frames, bossDeathFrames(boss, pos)...)
	}

	return Definition{Interval: 120, Frames: frames}
}

// heroAt renders the hero standing alone at pos (green).
func heroAt(hero string, pos int) string {
	return defenderRow(map[int]defenderCell{pos: {hero, ansiGreen}})
}

// approachFrames renders the actor entering from the left and moving one
// position right per frame until it stands next to the hero. When slow is
// true (the turtle hero) every position is held for two frames, so the actor
// takes twice as long to arrive.
func approachFrames(hero string, heroPos int, actor string, slow bool) []string {
	var frames []string
	for pos := 0; pos < heroPos; pos++ {
		frames = append(frames, defenderRow(map[int]defenderCell{
			pos:     {actor, ""},
			heroPos: {hero, ansiGreen},
		}))
		if slow {
			frames = append(frames, defenderRow(map[int]defenderCell{
				pos:     {actor, ""},
				heroPos: {hero, ansiGreen},
			}))
		}
	}
	return frames
}

// smokeKillFrames renders an enemy being destroyed at the hero's position:
// sparks, smoke, dust — NO fire. The hero survives (it reappears one position
// left in the next approach frame).
func smokeKillFrames(pos int) []string {
	return []string{
		defenderRow(map[int]defenderCell{pos: {"✨", ansiYellow}}),
		defenderRow(map[int]defenderCell{pos: {"💨", ansiGray}}),
		defenderRow(map[int]defenderCell{pos: {"🌫️", ansiGray}}),
	}
}

// suspenseFrames renders a quiet suspense break: drum-roll dots accumulate
// one at a time on the left — a small pause that builds anticipation before
// the next actor strikes. The screen goes quiet (no hero shown).
func suspenseFrames() []string {
	return []string{
		defenderRow(map[int]defenderCell{0: {"•", ""}}),
		defenderRow(map[int]defenderCell{0: {"•", ""}, 1: {"•", ""}}),
		defenderRow(map[int]defenderCell{0: {"•", ""}, 1: {"•", ""}, 2: {"•", ""}}),
	}
}

// moonFrames renders the moon coming up on the left horizon, arcing across
// the sky, and going under on the right. A pure-sky interlude: the hero is
// hidden while the moon passes (it would otherwise overlap the hero cell).
func moonFrames() []string {
	positions := []int{0, 3, 6, 9, 12}
	frames := make([]string, 0, len(positions))
	for _, p := range positions {
		frames = append(frames, defenderRow(map[int]defenderCell{p: {"🌙", ""}}))
	}
	return frames
}

// bossDeathFrames renders the boss destroying the hero (and itself) at pos.
// A bomb kills with FIRE (🔥 ☁️ ☢️ 🌫️); a comet kills with smoke only (☁️ 🌫️).
func bossDeathFrames(boss string, pos int) []string {
	if boss == "💣" {
		return []string{
			defenderRow(map[int]defenderCell{pos: {"🔥", ansiRed}}),
			defenderRow(map[int]defenderCell{pos: {"☁️", ansiGray}}),
			defenderRow(map[int]defenderCell{pos: {"☢️", ansiRed}}),
			defenderRow(map[int]defenderCell{pos: {"🌫️", ansiGray}}),
		}
	}
	return []string{
		defenderRow(map[int]defenderCell{pos: {"☁️", ansiGray}}),
		defenderRow(map[int]defenderCell{pos: {"🌫️", ansiGray}}),
	}
}

// defenderRow renders one defWidth-cell frame: the given dynamic cells over a
// Braille-dot background, with the ANSI color applied per cell when set.
func defenderRow(actors map[int]defenderCell) string {
	var b strings.Builder
	for i := 0; i < defWidth; i++ {
		c, ok := actors[i]
		if !ok {
			b.WriteString(dot)
			continue
		}
		if c.color != "" {
			b.WriteString(c.color)
		}
		b.WriteString(c.glyph)
		if c.color != "" {
			b.WriteString(ansiReset)
		}
	}
	return b.String()
}

// lcg derives a deterministic pseudo-random value from seed — a stable
// xorshift-style mix so the story is identical on every run (and testable).
func lcg(seed uint64) uint64 {
	state := seed*6364136223846793005 + 1442695040888963407
	state ^= state >> 32
	return state
}

// deterministicShuffle returns a copy of items in a pseudo-random order
// derived from seed (Fisher-Yates with the lcg mixer).
func deterministicShuffle(items []string, seed uint64) []string {
	out := append([]string(nil), items...)
	state := lcg(seed)
	for i := len(out) - 1; i > 0; i-- {
		state = lcg(state)
		j := int(state % uint64(i+1))
		out[i], out[j] = out[j], out[i]
	}
	return out
}
