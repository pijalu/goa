// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package spinner

import "strings"

// Defender returns the "Defender" story spinner: a tiny evolving terminal
// game rather than a traditional spinner. A hero stands fixed near the right
// edge of a 7-cell line while enemy objects stream in from the left, moving
// one position right every frame until they collide with the hero and are
// destroyed (💥 ✨ 💨 🌫️). After every enemy type has been used once, a final
// boss (☄️ or 💣) reaches the hero and destroys BOTH (🔥 ☁️ ☢️ 🌫️), then the
// next hero steps up:
//
//	heroes:  🐐 goat → 🦆 duck → 🐟 fish → 🦖 dinosaur → 🏎️ race car
//	enemies: 🐛 🐍 🪨 🧱 🐟 🕷️ 🦂 👾  (shuffled per cycle, no repeat until all used)
//
// Every enemy is destroyed; the hero survives enemies and only falls to the
// final boss. No hero repeats until all five have been used. The animation
// loops forever at 120 ms per frame.
//
// ANSI colors (applied around the glyphs; color terminals render the emoji
// natively, monochrome terminals pick them up): red explosions, yellow
// sparks, gray smoke/dust, green heroes.
const (
	defWidth   = 7 // animation area: 7 cells wide
	defHeroPos = 5 // hero stands near the right edge (one cell margin)

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
	heroes := []string{"🐐", "🦆", "🐟", "🦖", "🏎️"}
	enemies := []string{"🐛", "🐍", "🪨", "🧱", "🐟", "🕷️", "🦂", "👾"}
	bosses := []string{"☄️", "💣"}

	var frames []string
	for cycle, hero := range heroes {
		// Fresh hero steps up at the right edge.
		frames = append(frames, defenderRow(map[int]defenderCell{
			defHeroPos: {hero, ansiGreen},
		}))

		// Each enemy type enters exactly once per cycle, in a deterministic
		// pseudo-random order (stable across runs, still varied).
		for _, enemy := range deterministicShuffle(enemies, uint64(cycle)) {
			frames = append(frames, approachFrames(hero, enemy)...)
			frames = append(frames, collisionFrames()...)
		}

		// Final boss: randomly (deterministically per cycle) a comet or a
		// bomb. It reaches the hero and destroys both.
		boss := bosses[int(lcg(uint64(cycle)+0xB055)%uint64(len(bosses)))]
		frames = append(frames, approachFrames(hero, boss)...)
		frames = append(frames, bossDestructionFrames()...)
	}

	return Definition{Interval: 120, Frames: frames}
}

// approachFrames renders the actor entering from the left and moving one
// position right per frame until it stands next to the hero.
func approachFrames(hero, actor string) []string {
	var frames []string
	for pos := 0; pos < defHeroPos; pos++ {
		frames = append(frames, defenderRow(map[int]defenderCell{
			pos:        {actor, ""},
			defHeroPos: {hero, ansiGreen},
		}))
	}
	return frames
}

// collisionFrames renders an enemy being destroyed at the hero's position:
// explosion, sparks, smoke, dust. The hero survives (it is back in the next
// approach frame).
func collisionFrames() []string {
	return []string{
		defenderRow(map[int]defenderCell{defHeroPos: {"💥", ansiRed}}),
		defenderRow(map[int]defenderCell{defHeroPos: {"✨", ansiYellow}}),
		defenderRow(map[int]defenderCell{defHeroPos: {"💨", ansiGray}}),
		defenderRow(map[int]defenderCell{defHeroPos: {"🌫️", ansiGray}}),
	}
}

// bossDestructionFrames renders the boss destroying the hero (and itself):
// fire, smoke cloud, mushroom cloud, settling dust. Both are gone.
func bossDestructionFrames() []string {
	return []string{
		defenderRow(map[int]defenderCell{defHeroPos: {"🔥", ansiRed}}),
		defenderRow(map[int]defenderCell{defHeroPos: {"☁️", ansiGray}}),
		defenderRow(map[int]defenderCell{defHeroPos: {"☢️", ansiRed}}),
		defenderRow(map[int]defenderCell{defHeroPos: {"🌫️", ansiGray}}),
	}
}

// defenderRow renders one 7-cell frame: the given dynamic cells over a
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
