// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package spinner

import "testing"

// TestGoat_Registered ensures the goat animation is always registered under
// the "goat" name, including when a user spinner.json replaces the builtin set.
func TestGoat_Registered(t *testing.T) {
	d, ok := Get("goat")
	if !ok {
		t.Fatal("goat spinner not registered")
	}
	if len(d.Frames) == 0 {
		t.Fatal("goat has no frames")
	}
}

// TestGoat_FramesExact pins the spec verbatim: the goat charges the wall,
// hits it, and the dust settles — all ten frames, in order.
func TestGoat_FramesExact(t *testing.T) {
	want := []string{
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
	}
	d := Goat()
	if len(d.Frames) != len(want) {
		t.Fatalf("goat frames = %v, want %v", d.Frames, want)
	}
	for i := range want {
		if d.Frames[i] != want[i] {
			t.Errorf("goat frame[%d] = %q, want %q", i, d.Frames[i], want[i])
		}
	}
}

// TestGoat_FrameGeometry pins the layout constraints: interval in the same
// 100-150 ms range as the Pac-Man animation and every frame exactly 4 cells
// wide with the Braille-dot background. Cell width counts grapheme clusters,
// so emoji with a variation selector (🌫️ = 🌫 + U+FE0F) still count as one
// cell.
func TestGoat_FrameGeometry(t *testing.T) {
	d := Goat()
	if iv := d.IntervalMS(); iv < 100 || iv > 150 {
		t.Errorf("goat interval = %dms, want 100-150ms", iv)
	}
	for i, f := range d.Frames {
		if w := visibleCells(f); w != 4 {
			t.Errorf("goat frame[%d] width = %d cells, want exactly 4: %q", i, w, f)
		}
	}
}

// TestGoat_Choreography ensures the full story arc is present: the wall
// frames the animation, the goat charges in, the headbutt bursts, and the
// dust settles before the loop restarts.
func TestGoat_Choreography(t *testing.T) {
	d := Goat()
	all := ""
	for _, f := range d.Frames {
		all += f
	}
	// The wall appears in 5 of the 10 frames (the charge frames plus the
	// rebuild frames); the goat in 4 (the two edge frames plus the two charge
	// frames).
	if n := countRune(all, '🧱'); n != 5 {
		t.Errorf("wall glyph appears %d times, want 5", n)
	}
	if n := countRune(all, '🐐'); n != 4 {
		t.Errorf("goat glyph appears %d times, want 4", n)
	}
	for _, r := range []rune{'💥', '🔥', '✨', '💫', '💨', '⬜'} {
		if !containsRune(all, r) {
			t.Errorf("story glyph %q missing from the animation", r)
		}
	}
	if !containsRune(all, '🌫') {
		t.Error("fog (🌫) missing from the animation")
	}
}

// visibleCells counts the visible cells of a frame, treating emoji variation
// selectors (U+FE0F) and zero-width joiners as non-spacing.
func visibleCells(s string) int {
	n := 0
	for _, r := range s {
		if r == '\uFE0F' || r == '\u200D' {
			continue
		}
		n++
	}
	return n
}

func countRune(s string, r rune) int {
	n := 0
	for _, c := range s {
		if c == r {
			n++
		}
	}
	return n
}

func containsRune(s string, r rune) bool { return countRune(s, r) > 0 }
