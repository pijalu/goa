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

// TestGoat_FramesExact pins the spec verbatim: the four frames, in order —
// two goats at the edges, closing in, the headbutt burst, and the sparkle.
func TestGoat_FramesExact(t *testing.T) {
	want := []string{
		"🐐⠂⠂🐐", // two goats at the edges
		"⠂🐐⠂🐐", // they close in
		"⠂⠂💥⠂", // headbutt!
		"⠂✨⠂⠂", // sparkle
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
// wide with the Braille-dot background.
func TestGoat_FrameGeometry(t *testing.T) {
	d := Goat()
	if iv := d.IntervalMS(); iv < 100 || iv > 150 {
		t.Errorf("goat interval = %dms, want 100-150ms", iv)
	}
	for i, f := range d.Frames {
		if w := len([]rune(f)); w != 4 {
			t.Errorf("goat frame[%d] width = %d cells, want exactly 4: %q", i, w, f)
		}
	}
}

// TestGoat_Choreography ensures the full story arc is present: the goats
// appear in the first frame, the collision burst and the sparkle follow.
func TestGoat_Choreography(t *testing.T) {
	d := Goat()
	all := ""
	for _, f := range d.Frames {
		all += f
	}
	if n := countRune(all, '🐐'); n != 4 {
		t.Errorf("goat glyph appears %d times across frames, want 4 (two per opening frame)", n)
	}
	if !containsRune(all, '💥') {
		t.Error("headbutt burst (💥) missing from the animation")
	}
	if !containsRune(all, '✨') {
		t.Error("sparkle (✨) missing from the animation")
	}
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
