// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package spinner

import (
	"strings"
	"testing"
)

var (
	defHeroes  = []string{"🐐", "🦭", "🐊", "🦘", "🧑", "🐔", "🧙", "🐢"}
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

// frameActors describes one frame: the hero glyph and its cell, plus the
// actor glyph to the left of the hero ("" when the hero stands alone). ok is
// false for frames without a hero (kills, moon, suspense, boss deaths).
func frameActors(f string) (hero string, heroIdx int, actor string, ok bool) {
	cells := defCells(f)
	if len(cells) != defWidth {
		return "", 0, "", false
	}
	heroIdx = -1
	for i, g := range cells {
		if isDefHero(g) {
			heroIdx = i
			hero = g
			break
		}
	}
	if heroIdx < 0 {
		return "", 0, "", false
	}
	for _, g := range cells[:heroIdx] {
		if g != dot {
			actor = g
			break
		}
	}
	return hero, heroIdx, actor, true
}

// defenderWaves reconstructs the per-hero story waves from the frame list.
type defenderWave struct {
	hero    string
	actors  []string
	heroPos []int
}

func defenderWaves(d Definition) []defenderWave {
	var waves []defenderWave
	cur := defenderWave{}
	lastActor := ""
	flush := func() {
		if cur.hero != "" {
			waves = append(waves, cur)
		}
	}
	for _, f := range d.Frames {
		hero, heroIdx, actor, ok := frameActors(f)
		if !ok {
			continue // kill / moon / suspense / boss-death frame
		}
		if actor == "" {
			// Hero standing alone (intro, or resting with dust to his right
			// after an advance): only a NEW hero starts a fresh wave.
			if cur.hero != hero {
				flush()
				cur = defenderWave{hero: hero}
				lastActor = ""
			}
			continue
		}
		if cur.hero != hero {
			flush()
			cur = defenderWave{hero: hero}
			lastActor = ""
		}
		// An actor occupies multiple approach frames (and the turtle holds
		// each position twice); record each actor once, when it changes.
		if actor != lastActor {
			cur.actors = append(cur.actors, actor)
			cur.heroPos = append(cur.heroPos, heroIdx)
			lastActor = actor
		}
	}
	flush()
	return waves
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
// range and every frame exactly defWidth cells wide.
func TestDefender_FrameGeometry(t *testing.T) {
	d := Defender()
	if iv := d.IntervalMS(); iv < 100 || iv > 150 {
		t.Errorf("defender interval = %dms, want 100-150ms", iv)
	}
	for i, f := range d.Frames {
		if w := visibleCells(f); w != defWidth {
			t.Errorf("frame[%d] width = %d cells, want exactly %d: %q", i, w, defWidth, f)
		}
	}
}

// TestDefender_HeroRotation pins the hero cycle: each hero appears in order
// at the hero position, and no hero repeats until all have been used.
func TestDefender_HeroRotation(t *testing.T) {
	d := Defender()
	var order []string
	seen := map[string]bool{}
	for _, f := range d.Frames {
		hero, _, _, ok := frameActors(f)
		if ok && !seen[hero] {
			seen[hero] = true
			order = append(order, hero)
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
	waves := defenderWaves(Defender())
	if len(waves) != len(defHeroes) {
		t.Fatalf("waves = %d, want %d (one per hero)", len(waves), len(defHeroes))
	}
	for i, w := range waves {
		if len(w.actors) != len(defEnemies)+1 {
			t.Errorf("wave %d (%s) has %d actors, want %d enemies + 1 boss",
				i, w.hero, len(w.actors), len(defEnemies))
			continue
		}
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
		boss := w.actors[len(defEnemies)]
		if boss != "☄️" && boss != "💣" {
			t.Errorf("wave %d final boss = %q, want ☄️ or 💣", i, boss)
		}
	}
}

// TestDefender_HeroMovesLeft pins the advance mechanic: the hero starts at
// defStartPos and moves exactly one position left after EVERY kill (the boss
// finds it at defStartPos-8); each new wave resets to the right.
func TestDefender_HeroMovesLeft(t *testing.T) {
	waves := defenderWaves(Defender())
	for i, w := range waves {
		if w.heroPos[0] != defStartPos {
			t.Errorf("wave %d (%s) hero starts at %d, want %d (reset to the right)",
				i, w.hero, w.heroPos[0], defStartPos)
		}
		for k, pos := range w.heroPos {
			want := defStartPos - k
			if pos != want {
				t.Errorf("wave %d (%s) enemy #%d hero position = %d, want %d (one step left per kill)",
					i, w.hero, k, pos, want)
			}
		}
	}
}

// TestDefender_TurtleIsSlow pins the turtle's slow mechanic: during the
// turtle wave every approach frame is held for two frames (each position
// doubled), so it takes twice as long before the turtle reaches its enemy.
func TestDefender_TurtleIsSlow(t *testing.T) {
	d := Defender()
	countApproach := func(hero string) (int, int) {
		n, first := 0, -1
		for i, f := range d.Frames {
			h, _, actor, ok := frameActors(f)
			if ok && h == hero && actor != "" {
				if first < 0 {
					first = i
				}
				n++
			}
		}
		return n, first
	}
	turtle, turtleFirst := countApproach("🐢")
	other, _ := countApproach("🐐")
	if turtle != 2*other {
		t.Errorf("turtle approach frames = %d, want %d (2x a normal hero)", turtle, 2*other)
	}
	// The very first approach frame of the turtle wave is held for two frames.
	if turtleFirst < 0 || d.Frames[turtleFirst] != d.Frames[turtleFirst+1] {
		t.Errorf("turtle approach does not hold its first position for two frames (slow) at frame %d", turtleFirst)
	}
}

// TestDefender_EnemyKillsAreSmokeNoFire pins the kill visuals: enemies die
// with sparks + smoke + dust only — no explosion, no fire. Fire appears
// EXCLUSIVELY in bomb deaths.
func TestDefender_EnemyKillsAreSmokeNoFire(t *testing.T) {
	d := Defender()
	waves := defenderWaves(d)
	bombCycles := 0
	for _, w := range waves {
		if w.actors[len(defEnemies)] == "💣" {
			bombCycles++
		}
	}
	all := strings.Join(d.Frames, "")
	count := func(r rune) int { return countRune(all, r) }

	if n := count('✨'); n != len(waves)*len(defEnemies) {
		t.Errorf("spark count = %d, want %d (one per enemy kill)", n, len(waves)*len(defEnemies))
	}
	if n := count('💨'); n != len(waves)*len(defEnemies) {
		t.Errorf("smoke count = %d, want %d (one per enemy kill)", n, len(waves)*len(defEnemies))
	}
	if n := count('🌫'); n != len(waves)*len(defEnemies)+3*len(waves) {
		t.Errorf("dust count = %d, want %d (enemy dust + boss dust + lingering afterglow)", n, len(waves)*len(defEnemies)+3*len(waves))
	}
	if n := count('🔥'); n != 2*bombCycles {
		t.Errorf("fire count = %d, want %d (fireball held two frames, ONLY in bomb deaths)", n, 2*bombCycles)
	}
	if n := count('☁'); n != len(waves) {
		t.Errorf("smoke-cloud count = %d, want %d (one per boss death)", n, len(waves))
	}
	if n := count('☢'); n != bombCycles {
		t.Errorf("mushroom-cloud count = %d, want %d (only with bombs)", n, bombCycles)
	}
}

// TestDefender_MoonAfterEveryThirdKill pins the braille moon interlude:
// after every 3rd enemy killed the moon crosses the sky — a sliver that waxes
// to a full disc at the apex and wanes, each position held twice (a slow
// pass), with the sky cleared to blanks around it. A pure-sky cutaway.
func TestDefender_MoonAfterEveryThirdKill(t *testing.T) {
	wantPos := []int{0, 2, 4, 6, 8, 10, 12}
	wantPhase := []string{dot, moonWax, moonBig, moonFull, moonBig, moonWax, dot}
	var wantInterlude []string
	for i, p := range wantPos {
		row := map[int]defenderCell{p: {wantPhase[i], ""}}
		if p > 0 {
			row[p-1] = defenderCell{blank, ""}
		}
		if p < defWidth-1 {
			row[p+1] = defenderCell{blank, ""}
		}
		wantInterlude = append(wantInterlude, defenderRow(row), defenderRow(row))
	}

	d := Defender()
	interludes := 0
	for i := 0; i < len(d.Frames); i++ {
		// Moon frames are identifiable by their cleared-sky blanks and denser
		// braille phases — nothing else in the story uses them.
		isMoon := false
		for _, g := range defCells(d.Frames[i]) {
			if g == blank || g == moonWax || g == moonBig || g == moonFull {
				isMoon = true
			}
		}
		if !isMoon {
			continue
		}
		if i+len(wantInterlude) > len(d.Frames) {
			t.Fatalf("moon interlude truncated at frame %d", i)
		}
		for k, want := range wantInterlude {
			if d.Frames[i+k] != want {
				t.Errorf("moon interlude %d frame %d mismatch\n got %q\nwant %q", interludes, k, d.Frames[i+k], want)
			}
		}
		interludes++
		i += len(wantInterlude) - 1
	}
	if want := len(defHeroes) * 2; interludes != want {
		t.Errorf("moon interludes = %d, want %d (after kills 3 and 6, per hero)", interludes, want)
	}
}

// TestDefender_HeroDust pins the advance dust: after EVERY kill the hero
// steps one position left and small braille dust dots kick up at his right
// side, drift right, and settle — exactly three frames, the dots thinning
// 3→2→1 (clipped at the right edge). The hero's advance positions march
// defStartPos-1 → defStartPos-8 across each wave.
func TestDefender_HeroDust(t *testing.T) {
	type run struct {
		hero   string
		pos    int
		frames []string
	}
	var runs []run
	cur := run{}
	flush := func() {
		if len(cur.frames) > 0 {
			runs = append(runs, cur)
			cur = run{}
		}
	}
	for _, f := range Defender().Frames {
		cells := defCells(f)
		hero, heroIdx := "", -1
		var dust []int
		bad := ""
		for i, g := range cells {
			switch {
			case g == dot:
			case isDefHero(g):
				hero, heroIdx = g, i
			case g == dustDot:
				dust = append(dust, i)
			default:
				bad = g
			}
		}
		// A dust frame: the hero plus at least one dust dot on his right.
		if hero == "" || len(dust) == 0 {
			flush()
			continue
		}
		if bad != "" {
			t.Errorf("unexpected glyph %q in dust frame %q", bad, f)
		}
		if cur.hero != hero {
			flush()
		}
		cur.hero, cur.pos = hero, heroIdx
		cur.frames = append(cur.frames, f)
	}
	flush()

	want := len(defHeroes) * len(defEnemies) // one dust run per kill
	if len(runs) != want {
		t.Fatalf("dust runs = %d, want %d (one per kill)", len(runs), want)
	}
	for _, r := range runs {
		wantDust := [][]int{
			clipCells([]int{r.pos + 1, r.pos + 2, r.pos + 3}),
			clipCells([]int{r.pos + 2, r.pos + 3}),
			clipCells([]int{r.pos + 3}),
		}
		wantFrames := 3
		if r.pos+3 >= defWidth {
			// Right-edge advance: the settling dot falls off the edge, so only
			// the kick-up and drift frames are visible.
			wantFrames = 2
		}
		if len(r.frames) != wantFrames {
			t.Errorf("%s dust run at pos %d has %d frames, want %d", r.hero, r.pos, len(r.frames), wantFrames)
			continue
		}
		for k, f := range r.frames {
			var got []int
			for i, g := range defCells(f) {
				if g == dustDot {
					got = append(got, i)
				}
			}
			if !equalInts(got, wantDust[k]) {
				t.Errorf("%s dust run frame %d dots at %v, want %v (drifting right) in %q",
					r.hero, k, got, wantDust[k], f)
			}
		}
	}
	// The advance positions march one step left per kill across each wave.
	byWave := map[string][]int{}
	for _, r := range runs {
		byWave[r.hero] = append(byWave[r.hero], r.pos)
	}
	for hero, poss := range byWave {
		for k, p := range poss {
			if want := defStartPos - 1 - k; p != want {
				t.Errorf("%s advance #%d at pos %d, want %d (one step left per kill)", hero, k, p, want)
			}
		}
	}
}

// clipCells drops cells beyond the right edge of the animation area.
func clipCells(cells []int) []int {
	out := []int{}
	for _, c := range cells {
		if c < defWidth {
			out = append(out, c)
		}
	}
	return out
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// TestDefender_BossDeathIsLongAndPauses pins the boss finale: a bomb sits,
// ticks, flashes, and blasts with a held fireball — a long explosion (blasts
// and flashes appear ONLY with bombs). After EVERY boss death the dust
// lingers and the screen falls silent for exactly two frames before the next
// hero steps up (it must not start too soon).
func TestDefender_BossDeathIsLongAndPauses(t *testing.T) {
	d := Defender()
	waves := defenderWaves(d)
	bombCycles := 0
	for _, w := range waves {
		if w.actors[len(defEnemies)] == "💣" {
			bombCycles++
		}
	}
	all := strings.Join(d.Frames, "")
	if n := countRune(all, '💥'); n != 2*bombCycles {
		t.Errorf("blast count = %d, want %d (blast held two frames, ONLY in bomb deaths)", n, 2*bombCycles)
	}
	if n := countRune(all, '⚡'); n != bombCycles {
		t.Errorf("flash count = %d, want %d (detonation flash ONLY in bomb deaths)", n, bombCycles)
	}
	// The screen falls silent for exactly two frames after every boss death.
	silence := 0
	for _, f := range d.Frames {
		quiet := true
		for _, g := range defCells(f) {
			if g != dot {
				quiet = false
				break
			}
		}
		if quiet {
			silence++
		}
	}
	if want := len(waves) * 2; silence != want {
		t.Errorf("silence frames = %d, want %d (a two-frame beat after each boss death)", silence, want)
	}
	// Every boss death is long and ends in silence before the new hero: walk
	// from each boss's first approach frame to the next wave's hero intro
	// (a hero with no actor beside it) and check the gap length and its
	// silent tail.
	prevBoss := false
	for i, f := range d.Frames {
		_, _, actor, ok := frameActors(f)
		isBoss := ok && (actor == "☄️" || actor == "💣")
		if isBoss && !prevBoss {
			j := i + 1
			for ; j < len(d.Frames); j++ {
				h, _, a, ok2 := frameActors(d.Frames[j])
				if ok2 && a == "" && h != "" {
					break
				}
			}
			minGap := 4 // comet: approach 3 + smoke 2 + afterglow 4 (lower bound)
			if actor == "💣" {
				minGap = 12 // bomb: tick 2 + flash 1 + blast 2 + fire 2 + cloud 3 + afterglow 4
			}
			if gap := j - i; gap < minGap {
				t.Errorf("boss death gap = %d frames, want >= %d (the explosion must take longer)", gap, minGap)
			}
			for _, k := range []int{j - 1, j - 2} {
				quiet := true
				for _, g := range defCells(d.Frames[k]) {
					if g != dot {
						quiet = false
						break
					}
				}
				if !quiet {
					t.Errorf("boss death at frame %d not followed by a silent pause before hero at frame %d", i, j)
				}
			}
		}
		prevBoss = isBoss
	}
}

// TestDefender_DustIsLowerDots pins the dust glyph: the hero's advance dust
// must ALWAYS be drawn with the lower dots of the braille cell (the bottom
// row: dot 3 and/or dot 6) — never middle or top dots. (TestDefender_HeroDust
// already pins that dust frames contain only the hero, the dust glyph, and
// the background dot.)
func TestDefender_DustIsLowerDots(t *testing.T) {
	lower := map[string]bool{"⠄": true, "⠠": true, "⠤": true}
	if !lower[dustDot] {
		t.Errorf("dustDot = %q, want a lower-dot braille glyph (⠄ ⠠ or ⠤)", dustDot)
	}
}

// TestDefender_SuspenseBreaks pins the suspense pauses: a quiet drum-roll
// (dots accumulating 1,2,3 on the left, no hero) before the 4th enemy and
// before the final boss of every cycle — two breaks per cycle.
func TestDefender_SuspenseBreaks(t *testing.T) {
	d := Defender()
	breaks := 0
	for i := 0; i < len(d.Frames); i++ {
		if !containsRune(d.Frames[i], '•') {
			continue
		}
		if i+2 >= len(d.Frames) {
			t.Fatalf("suspense break truncated at frame %d", i)
		}
		for k, want := range []int{1, 2, 3} {
			cells := defCells(d.Frames[i+k])
			n := 0
			for _, g := range cells {
				if g == "•" {
					n++
				}
				if isDefHero(g) {
					t.Errorf("hero on screen during suspense frame %d: %q", i+k, d.Frames[i+k])
				}
			}
			if n != want {
				t.Errorf("suspense frame %d has %d drum dots, want %d", i+k, n, want)
			}
		}
		breaks++
		i += 2
	}
	if want := len(defHeroes) * 2; breaks != want {
		t.Errorf("suspense breaks = %d, want %d (before 4th enemy + boss, per cycle)", breaks, want)
	}
}

// TestDefender_SuspensePrecedesBoss pins the final suspense beat: every boss
// approach is directly preceded by the drum-roll.
func TestDefender_SuspensePrecedesBoss(t *testing.T) {
	d := Defender()
	bossApproaches := 0
	prevBoss := false
	for i, f := range d.Frames {
		_, _, actor, ok := frameActors(f)
		isBoss := ok && (actor == "☄️" || actor == "💣")
		if isBoss && !prevBoss {
			bossApproaches++
			if i == 0 || !containsRune(d.Frames[i-1], '•') {
				t.Errorf("boss approach at frame %d not preceded by a suspense break", i)
			}
		}
		prevBoss = isBoss
	}
	if want := len(defHeroes); bossApproaches != want {
		t.Errorf("boss approaches = %d, want %d", bossApproaches, want)
	}
}

// TestDefender_Colors ensures the requested ANSI palette is applied: yellow
// sparks, gray smoke/dust, red fire (bomb deaths), green heroes.
func TestDefender_Colors(t *testing.T) {
	joined := strings.Join(Defender().Frames, "")
	for _, want := range []string{ansiRed, ansiYellow, ansiGray, ansiGreen, ansiReset} {
		if !strings.Contains(joined, want) {
			t.Errorf("frames missing ANSI sequence %q", want)
		}
	}
}

// TestDefender_BothBossesAcrossCycles ensures the story is not stuck on one
// boss type: over all waves both ☄️ and 💣 appear (deterministic but varied).
func TestDefender_BothBossesAcrossCycles(t *testing.T) {
	seen := map[string]bool{}
	for _, w := range defenderWaves(Defender()) {
		seen[w.actors[len(defEnemies)]] = true
	}
	if !seen["☄️"] || !seen["💣"] {
		t.Errorf("boss variety missing: saw %v, want both ☄️ and 💣 across cycles", seen)
	}
}
