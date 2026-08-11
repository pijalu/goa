// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package spinner

import (
	"regexp"
	"strings"
	"testing"
)

var ansiSeq = regexp.MustCompile("\x1b\\[[0-9;]*m")

func stripANSI(s string) string { return ansiSeq.ReplaceAllString(s, "") }

// TestPacman_Registered ensures the generated animation is always registered
// under the "pacman" name, including when a user spinner.json replaces the
// builtin set.
func TestPacman_Registered(t *testing.T) {
	d, ok := Get("pacman")
	if !ok {
		t.Fatal("pacman spinner not registered")
	}
	if len(d.Frames) == 0 {
		t.Fatal("pacman has no frames")
	}
}

// TestPacman_FrameGeometry pins the spec's hard constraints: interval in the
// 100-150 ms range, every frame exactly 6 visible cells wide with exactly one
// Pac-Man and one ghost.
func TestPacman_FrameGeometry(t *testing.T) {
	d := Pacman()
	if iv := d.IntervalMS(); iv < 100 || iv > 150 {
		t.Errorf("pacman interval = %dms, want 100-150ms", iv)
	}
	if len(d.Frames) < 20 {
		t.Errorf("pacman has only %d frames, want a full chase loop", len(d.Frames))
	}
	for i, f := range d.Frames {
		plain := stripANSI(f)
		if w := len([]rune(plain)); w != 6 {
			t.Errorf("frame[%d] width = %d cells, want exactly 6: %q", i, w, plain)
		}
		pacs := strings.Count(plain, pacOpen) + strings.Count(plain, pacHalf) + strings.Count(plain, pacNearly)
		ghosts := strings.Count(plain, ghostNormal) + strings.Count(plain, ghostFright)
		if pacs != 1 || ghosts != 1 {
			t.Errorf("frame[%d] has pac=%d ghost=%d, want exactly 1/1: %q", i, pacs, ghosts, plain)
		}
	}
}

// TestPacman_PhaseSequence pins the spec's exact chase choreography: phase 1
// opens with Pac-Man at the far left chasing a fixed ghost at the far right,
// ends with Pac-Man touching the ghost, then phase 2 reverses with the ghost
// chasing back to the far left before the loop restarts.
func TestPacman_PhaseSequence(t *testing.T) {
	d := Pacman()
	plain := func(i int) string { return stripANSI(d.Frames[i]) }

	// Phase 1 open: Pac-Man at cell 0, ghost at cell 5.
	if plain(0) != "ᗧ⠂⠂⠂⠂👻" {
		t.Errorf("first frame = %q, want %q", plain(0), "ᗧ⠂⠂⠂⠂👻")
	}
	// Phase 1 end: Pac-Man reaches the ghost (adjacent at cells 4 and 5).
	found := false
	for _, f := range d.Frames {
		if stripANSI(f) == "⠂⠂⠂⠂ᗧ👻" {
			found = true
			break
		}
	}
	if !found {
		t.Error("phase 1 never reaches '⠂⠂⠂⠂ᗧ👻' (Pac-Man must touch the ghost)")
	}
	// Phase 2 open: ghost at cell 3 chasing Pac-Man fixed at cell 4.
	found = false
	for _, f := range d.Frames {
		if stripANSI(f) == "⠂⠂⠂👻ᗧ⠂" {
			found = true
			break
		}
	}
	if !found {
		t.Error("phase 2 never shows '⠂⠂⠂👻ᗧ⠂' (ghost must chase Pac-Man)")
	}
	// Phase 2 end: ghost reaches the far left (normal or frightened — the
	// flicker cycle may end on either glyph).
	last := plain(len(d.Frames) - 1)
	if last != "👻⠂⠂⠂ᗧ⠂" && last != "👾⠂⠂⠂ᗧ⠂" {
		t.Errorf("last frame = %q, want ghost at the far left (\"👻⠂⠂⠂ᗧ⠂\" or \"👾⠂⠂⠂ᗧ⠂\")", last)
	}
}

// TestPacman_Colors ensures the optional ANSI coloring is applied: bright
// yellow Pac-Man, cyan ghost, dark-gray dots.
func TestPacman_Colors(t *testing.T) {
	d := Pacman()
	joined := strings.Join(d.Frames, "")
	for _, want := range []string{ansiPac, ansiGhost, ansiDot, ansiReset} {
		if !strings.Contains(joined, want) {
			t.Errorf("frames missing ANSI sequence %q", want)
		}
	}
	// Frightened ghost mode (👾) must appear during the return phase.
	if !strings.Contains(joined, ghostFright) {
		t.Error("frightened ghost (👾) never appears")
	}
	// Mouth cycle must include the half-open and nearly-closed glyphs.
	for _, m := range []string{pacHalf, pacNearly} {
		if !strings.Contains(joined, m) {
			t.Errorf("mouth cycle missing glyph %q", m)
		}
	}
}
