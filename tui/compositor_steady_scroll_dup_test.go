// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// TestCompositor_ChromeChangeDoesNotDuplicateScrollback reproduces the
// duplicate-row bug seen at the start of a conversation with the goal/steering
// bubble visible. A bottom-chrome height change (steering/goal bubble
// appearing or clearing) while the transcript scrolls used to double-write
// rows (repaintWindow redrawing the row emitSteadyScroll just emitted).
//
// Invariant (updated for bugs.md "Slow performance on very large
// conversations"): chrome changes no longer wipe scrollback (\x1b[3J) — the
// old geometry reset destroyed the user's terminal history AND re-emitted the
// whole transcript per keystroke. The incremental path keeps a small,
// bounded overlap across the scrollback↔screen boundary on a chrome SHRINK
// (the window reveals up to Δchrome already-scrolled rows; sanctioned by
// windowTop's "dips below scrollTop" regime and the quota-stream assertion).
// What must NEVER happen: a row lost outright, a row duplicated WITHIN
// scrollback, or a row duplicated WITHIN the visible window (the actual
// corruption symptoms).
func TestCompositor_ChromeChangeDoesNotDuplicateScrollback(t *testing.T) {
	const (
		termH      = 10
		w          = 40
		baseChrome = 2
		historyN   = 18
	)
	term := &fakeTerminal{w: w, h: termH}
	comp := NewCompositor(term)

	history := make([]string, historyN)
	for i := range history {
		history[i] = "row-" + itoaStr(i)
	}
	mkScene := func(n, chromeH int) *Scene {
		content := append([]string{}, history[:n]...)
		for i := 0; i < chromeH; i++ {
			content = append(content, "CHROME")
		}
		return &Scene{TerminalW: w, TerminalH: termH, ChromeHeight: chromeH,
			Layers: []Layer{{Name: "c", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: w, H: len(content)}, Content: content}}}
	}

	for n := 1; n <= historyN-4; n++ {
		comp.Render(mkScene(n, baseChrome))
	}
	comp.Render(mkScene(historyN-2, baseChrome+3)) // bubble appears -> reset
	comp.Render(mkScene(historyN, baseChrome+3))   // grow with bubble
	comp.Render(mkScene(historyN, baseChrome))     // bubble clears -> reset

	// Replay the byte stream. Chrome changes no longer wipe scrollback, so a
	// single emulator accumulates the whole session — exactly what a real
	// terminal shows.
	emu := NewTermEmulator(termH, w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}
	scrollback := emu.Scrollback()
	var screen []string
	for r := 0; r < termH; r++ {
		screen = append(screen, strings.TrimSpace(emu.Visible(r)))
	}
	// Exact-row matching avoids "row-1" matching "row-10".."row-17".
	countIn := func(hay []string, want string) int {
		n := 0
		for _, r := range hay {
			if strings.TrimSpace(r) == want {
				n++
			}
		}
		return n
	}
	dump := "\n--- screen ---\n" + strings.Join(screen, "\n") + "\n--- scrollback ---\n" + strings.Join(scrollback, "\n")
	for i := 0; i < historyN; i++ {
		row := "row-" + itoaStr(i)
		inSB := countIn(scrollback, row)
		onScreen := countIn(screen, row)
		switch {
		case inSB+onScreen == 0:
			t.Errorf("transcript row %q LOST from the terminal%s", row, dump)
		case inSB > 1:
			t.Errorf("transcript row %q duplicated WITHIN scrollback (%d times) — the watermark re-emitted it%s", row, inSB, dump)
		case onScreen > 1:
			t.Errorf("transcript row %q duplicated WITHIN the visible window (%d times) — visible corruption%s", row, onScreen, dump)
		}
	}
}
