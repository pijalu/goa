// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package spinner

import (
	"strings"
	"testing"
)

var (
	defHeroes  = []string{"🐐", "🦆", "🐟", "🦖", "🏎️"}
	defEnemies = map[string]bool{"🐛": true, "🐍": true, "🪨": true, "🧱": true, "🐟": true, "🕷️": true, "🦂": true, "👾": true}
)

// defCells splits a frame into its visible cells (ANSI stripped, emoji
// variation selectors attached to their base glyph).
func defCells(s string) []string {
	plain := stripANSI(s)
	var cells []string
	cur := ""
	for _, r := range plain {
		if r == '\uFE0F' {
			cur += string(r)
			continue
		}
		if cur != "" {
			cells = append(cells, cur)
		}
		cur = string(r)
	}
	if cur != "" {
		cells = append(cells, cur)
	}
	return cells
}

func isDefHero(g string) bool {
	for _, h := range defHeroes {
		if g == h {
			return true
		}
	}
	return false
}

func TestDefender_Registered(t *testing.T) {
	d, ok := Get("defender")
	if !ok {
		t.Fatal("defender spinner not registered")
	}
	if len(d.Frames) == 0 {
		t.Fatal("defender has no frames")
	}
}

// TestDefender_FrameGeometry pins the layout: interval in the 100-150 ms
// range and every frame exactly 7 cells wide.
func TestDefender_FrameGeometry(t *testing.T) {
	d := Defender()
	if iv := d.IntervalMS(); iv < 100 || iv > 150 {
		t.Errorf("defender interval = %dms, want 100-150ms", iv)
	}
	for i, f := range d.Frames {
		if w := visibleCells(f); w != 7 {
			t.Errorf("frame[%d] width = %d cells, want exactly 7: %q", i, w, f)
		}
	}
}

// TestDefender_HeroRotation pins the hero cycle: each hero appears at the
// hero position in order, and no hero repeats until all five have been used.
func TestDefender_HeroRotation(t *testing.T) {
	d := Defender()
	var order []string
	seen := map[string]bool{}
	for _, f := range d.Frames {
		cells := defCells(f)
		if len(cells) != 7 {
			continue
		}
		g := cells[defHeroPos]
		if isDefHero(g) && !seen[g] {
			seen[g] = true
			order = append(order, g)
		}
	}
	if len(order) != len(defHeroes) {
		t.Fatalf("distinct heroes = %v, want all %v used once", order, defHeroes)
	}
	for i, want := range defHeroes {
		if order[i] != want {
			t.Errorf("hero order[%d] = %q, want %q (rotation %v)", i, order[i], want, defHeroes)
		}
	}
}

// TestDefender_EnemyRounds pins the enemy stream: within every hero wave the
// eight enemy types each enter exactly once (no repeat until all used), and
// the wave closes with a final boss (☄️ or 💣) that reaches the hero.
func TestDefender_EnemyRounds(t *testing.T) {
	d := Defender()
	type wave struct {
		hero   string
		actors []string
	}
	var waves []wave
	cur := wave{}
	lastActor := ""
	for _, f := range d.Frames {
		cells := defCells(f)
		if len(cells) != 7 {
			continue
		}
		hero := cells[defHeroPos]
		if !isDefHero(hero) {
			continue // collision / destruction frame
		}
		actor := ""
		for _, g := range cells[:defHeroPos] {
			if g != dot {
				actor = g
				break
			}
		}
		if actor == "" {
			// hero intro: start a fresh wave
			if cur.hero != "" {
				waves = append(waves, cur)
			}
			cur = wave{hero: hero}
			lastActor = ""
			continue
		}
		if cur.hero != hero {
			if cur.hero != "" {
				waves = append(waves, cur)
			}
			cur = wave{hero: hero}
			lastActor = ""
		}
		// An enemy/boss occupies multiple approach frames; record each actor
		// once, when it changes.
		if actor != lastActor {
			cur.actors = append(cur.actors, actor)
			lastActor = actor
		}
	}
	if cur.hero != "" {
		waves = append(waves, cur)
	}

	if len(waves) != len(defHeroes) {
		t.Fatalf("waves = %d, want %d (one per hero)", len(waves), len(defHeroes))
	}
	for i, w := range waves {
		if len(w.actors) != len(defEnemies)+1 {
			t.Errorf("wave %d (%s) has %d actors, want %d enemies + 1 boss",
				i, w.hero, len(w.actors), len(defEnemies))
			continue
		}
		// First 8: every enemy exactly once.
		seen := map[string]bool{}
		for _, a := range w.actors[:len(defEnemies)] {
			if !defEnemies[a] {
				t.Errorf("wave %d actor %q is not an enemy", i, a)
			}
			if seen[a] {
				t.Errorf("wave %d repeats enemy %q before all enemies were used", i, a)
			}
			seen[a] = true
		}
		// Last: the final boss.
		boss := w.actors[len(defEnemies)]
		if boss != "☄️" && boss != "💣" {
			t.Errorf("wave %d final boss = %q, want ☄️ or 💣", i, boss)
		}
	}
}

// TestDefender_DestructionSequences pins the collision choreography: every
// enemy dies with 💥 ✨ 💨 🌫️ at the hero position; every boss takes the hero
// with it in 🔥 ☁️ ☢️ 🌫️.
func TestDefender_DestructionSequences(t *testing.T) {
	d := Defender()
	counts := map[string]int{}
	for _, f := range d.Frames {
		cells := defCells(f)
		if len(cells) != 7 {
			continue
		}
		g := cells[defHeroPos]
		counts[g]++
	}
	waves := len(defHeroes)
	enemies := len(defEnemies)
	// Enemy collisions: 4 frames each; boss: fire/cloud/mushroom at the end.
	if counts["💥"] != waves*enemies || counts["✨"] != waves*enemies || counts["💨"] != waves*enemies {
		t.Errorf("enemy collision counts wrong: %v (want %d per glyph)",
			map[string]int{"💥": counts["💥"], "✨": counts["✨"], "💨": counts["💨"]}, waves*enemies)
	}
	if counts["🔥"] != waves || counts["☁️"] != waves || counts["☢️"] != waves {
		t.Errorf("boss destruction counts wrong: %v (want %d per glyph)",
			map[string]int{"🔥": counts["🔥"], "☁️": counts["☁️"], "☢️": counts["☢️"]}, waves)
	}
	// Dust closes both the enemy collision and the boss sequence.
	if counts["🌫️"] != waves*enemies+waves {
		t.Errorf("dust count = %d, want %d (enemy + boss dust)", counts["🌫️"], waves*enemies+waves)
	}
}

// TestDefender_Colors ensures the requested ANSI palette is applied: red
// explosions, yellow sparks, gray smoke/dust, green heroes.
func TestDefender_Colors(t *testing.T) {
	joined := strings.Join(Defender().Frames, "")
	for _, want := range []string{ansiRed, ansiYellow, ansiGray, ansiGreen, ansiReset} {
		if !strings.Contains(joined, want) {
			t.Errorf("frames missing ANSI sequence %q", want)
		}
	}
}

// TestDefender_BothBossesAcrossCycles ensures the story is not stuck on one
// boss type: over five waves both ☄️ and 💣 appear (deterministic but varied).
func TestDefender_BothBossesAcrossCycles(t *testing.T) {
	d := Defender()
	seen := map[string]bool{}
	for _, f := range d.Frames {
		cells := defCells(f)
		if len(cells) != 7 {
			continue
		}
		if !isDefHero(cells[defHeroPos]) {
			continue
		}
		for _, g := range cells[:defHeroPos] {
			if g == "☄️" || g == "💣" {
				seen[g] = true
			}
		}
	}
	if !seen["☄️"] || !seen["💣"] {
		t.Errorf("boss variety missing: saw %v, want both ☄️ and 💣 across cycles", seen)
	}
}
