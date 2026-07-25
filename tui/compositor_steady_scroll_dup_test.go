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
// appearing or clearing) while the transcript scrolls triggers a
// frameGeometryReset that wipes the screen + scrollback (\x1b[2J\x1b[H\x1b[3J)
// and re-emits the transcript. The incremental scroll path also double-writes
// the bottom row (repaintWindow redrawing the row emitSteadyScroll just
// emitted). After the fix, replaying the byte stream through a terminal that
// honors the wipe leaves every transcript row exactly once.
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

	// Replay the byte stream, honoring the scrollback wipe (\x1b[3J) the way a
	// real terminal does: at each wipe, the visible rows about to be re-emitted
	// become the new scrollback baseline, so start a fresh emulator. (A real
	// terminal drops pre-wipe scrollback; the emulator keeps it appended.)
	var emu *TermEmulator
	emu = NewTermEmulator(termH, w)
	for _, wr := range term.Writes() {
		if strings.Contains(wr, "\x1b[3J") {
			emu = NewTermEmulator(termH, w)
		}
		emu.Process(wr)
	}
	// Collect exact terminal rows (scrollback + screen), trimmed. Exact-row
	// matching avoids "row-1" matching "row-10".."row-17" as a substring.
	var rows []string
	for _, s := range emu.Scrollback() {
		rows = append(rows, strings.TrimSpace(s))
	}
	for r := 0; r < termH; r++ {
		rows = append(rows, strings.TrimSpace(emu.Visible(r)))
	}

	count := func(want string) int {
		n := 0
		for _, r := range rows {
			if r == want {
				n++
			}
		}
		return n
	}
	for i := 0; i < historyN; i++ {
		row := "row-" + itoaStr(i)
		if c := count(row); c != 1 {
			t.Errorf("transcript row %q appears %d times in terminal (want exactly 1)\n--- rows ---\n%s", row, c, strings.Join(rows, "\n"))
		}
	}
}
