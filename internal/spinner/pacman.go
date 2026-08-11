// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package spinner

import "strings"

// Unicode Pac-Man / Ghost Ping-Pong waiting animation.
//
// A 6-cell line, always exactly 6 visible cells wide, with the background
// filled with Braille dots (⠂, U+2802):
//
//	⠂⠂⠂⠂⠂⠂
//
// Phase 1 — CHASE_RIGHT: Pac-Man (ᗧ) starts at the far left and moves right
// while the ghost (👻) stays fixed at the far right:
//
//	ᗧ⠂⠂⠂⠂👻  ⠂ᗧ⠂⠂⠂👻  ⠂⠂ᗧ⠂⠂👻  ⠂⠂⠂ᗧ⠂👻  ⠂⠂⠂⠂ᗧ👻
//
// Phase 2 — CHASE_LEFT: the ghost chases Pac-Man back to the left while
// Pac-Man stays near the right side, then the loop restarts:
//
//	⠂⠂⠂👻ᗧ⠂  ⠂⠂👻⠂ᗧ⠂  ⠂👻⠂⠂ᗧ⠂  👻⠂⠂⠂ᗧ⠂
//
// While moving, Pac-Man chews through its mouth cycle (ᗧ open → ᗣ half-open →
// ᗤ nearly closed → ᗣ); during the return the ghost flickers between normal
// (👻) and frightened (👾) mode. Colored via ANSI: bright-yellow Pac-Man,
// cyan ghost, dark-gray dots.
//
// This is goa's waiting animation, shown while the agent processes a request.
const (
	pacOpen     = "ᗧ"  // open mouth
	pacHalf     = "ᗣ"  // half-open mouth
	pacNearly   = "ᗤ"  // nearly closed mouth
	ghostNormal = "👻" // normal ghost
	ghostFright = "👾" // frightened ghost
	dot         = "⠂"  // braille dot (U+2802)

	ansiPac    = "\x1b[93m" // bright yellow
	ansiGhost  = "\x1b[36m" // cyan
	ansiFright = "\x1b[35m" // magenta
	ansiDot    = "\x1b[90m" // dark gray
	ansiReset  = "\x1b[0m"
)

// Pacman returns the Pac-Man / ghost ping-pong animation definition. The
// interval is 120 ms (spec: 100-150 ms) and the loop runs indefinitely —
// the TUI drives the frames while the agent is working.
func Pacman() Definition {
	var frames []string

	// Phase 1 — CHASE_RIGHT: Pac-Man moves from cell 0 to cell 4 while the
	// ghost holds cell 5 (the far right). Each position cycles the mouth.
	chew := []string{pacOpen, pacHalf, pacNearly, pacHalf}
	for p := 0; p <= 4; p++ {
		for _, mouth := range chew {
			frames = append(frames, chaseFrame(p, 5, mouth, ghostNormal))
		}
	}

	// Phase 2 — CHASE_LEFT: the ghost moves from cell 3 back to cell 0 while
	// Pac-Man holds cell 4. The ghost flickers between normal and frightened.
	flicker := []string{ghostNormal, ghostFright}
	for g := 3; g >= 0; g-- {
		for _, gh := range flicker {
			frames = append(frames, chaseFrame(4, g, pacOpen, gh))
		}
	}

	return Definition{Interval: 120, Frames: frames}
}

// chaseFrame renders one 6-cell frame: pac at cell p, ghost at cell g, the
// remaining cells Braille dots. Cells are colored individually so the colors
// survive any surrounding SGR styling (dim status lines, footer highlights).
func chaseFrame(p, g int, pacGlyph, ghostGlyph string) string {
	cells := make([]string, 6)
	for i := range cells {
		cells[i] = dot
	}
	cells[p] = pacGlyph
	cells[g] = ghostGlyph

	var b strings.Builder
	for i, c := range cells {
		switch {
		case i == p:
			b.WriteString(ansiPac)
		case i == g:
			if ghostGlyph == ghostFright {
				b.WriteString(ansiFright)
			} else {
				b.WriteString(ansiGhost)
			}
		default:
			b.WriteString(ansiDot)
		}
		b.WriteString(c)
		b.WriteString(ansiReset)
	}
	return b.String()
}
