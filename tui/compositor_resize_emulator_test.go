// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// TestCompositor_ResizeReflowsHistoryIntoScrollback is the emulator-backed
// repro for the resize bug: with a bottom-anchored chat viewport (content
// taller than the screen, chrome pinned at the bottom), a width resize must
// re-emit the off-screen history at the new width so the terminal scrollback
// still contains it. Byte-level tests (TestCompositor_WidthResizeResetsScrollback)
// pass, but they never check what actually lands in the terminal — this test
// replays the writes through TermEmulator and inspects scrollback + screen.
func TestCompositor_ResizeReflowsHistoryIntoScrollback(t *testing.T) {
	const (
		termH = 10
		oldW  = 40
		newW  = 60
		// Chat viewport height (transcript area); 2 chrome rows pinned below.
		chatH     = termH - 2
		historyN  = 25 // history lines: more than the visible chat area
		chromeRow = "FOOTER"
	)

	term := &fakeTerminal{w: oldW, h: termH}
	comp := NewCompositor(term)

	// Chat layer: the full transcript (ChatViewport.Render returns every entry;
	// the compositor window-manages the overflow). Bottom-anchored within the
	// reserved viewport height when content is shorter than the screen.
	history := make([]string, historyN)
	for i := range history {
		history[i] = "history-line-" + itoaStr(i)
	}

	mkScene := func(n int, w int) *Scene {
		chat := Layer{
			Name: "chat", Kind: LayerBase,
			Rect:    Rect{X: 0, Y: 0, W: w, H: n},
			Content: history[:n],
		}
		chrome := Layer{
			Name: "footer", Kind: LayerBase,
			Rect:    Rect{X: 0, Y: n, W: w, H: 2},
			Content: []string{chromeRow, chromeRow},
		}
		return &Scene{TerminalW: w, TerminalH: termH, ChromeHeight: 2, Layers: []Layer{chat, chrome}}
	}

	// Grow the transcript one line at a time across the screen-full threshold,
	// mirroring a live session (each render advances the scroll watermark).
	for n := 1; n <= historyN; n++ {
		comp.Render(mkScene(n, oldW))
	}

	// Sanity: before resize, the oldest history must already be in the
	// terminal scrollback (it scrolled off the top long ago).
	emu := NewTermEmulator(termH, oldW)
	for _, w := range term.writes {
		emu.Process(w)
	}
	preSB := strings.Join(emu.Scrollback(), "\n")
	if !strings.Contains(preSB, "history-line-0") {
		t.Fatalf("pre-resize scrollback missing oldest history (watermark broken):\n%s", preSB)
	}

	// Width resize: 40 -> 60. Same full transcript re-rendered at the new width.
	term.w = newW
	resizeFrameIdx := len(term.writes)
	comp.Render(mkScene(historyN, newW))
	newWrites := term.writes[resizeFrameIdx:]

	// The emulator cannot change width; emulate the user's terminal by
	// replaying the resize frame into a NEW emulator at the new width (the
	// frame starts with \x1b[2J\x1b[H\x1b[3J, so prior state is irrelevant).
	emu2 := NewTermEmulator(termH, newW)
	for _, w := range newWrites {
		emu2.Process(w)
	}

	// After resize, the earliest history line must be reachable: either on the
	// (taller virtual) screen or in the terminal scrollback — NOT blank.
	sb := strings.Join(emu2.Scrollback(), "\n")
	scr := ""
	for r := 0; r < termH; r++ {
		scr += emu2.Visible(r) + "\n"
	}
	if !strings.Contains(sb+scr, "history-line-0") {
		t.Errorf("oldest history line lost on width resize\n--- scrollback ---\n%s\n--- screen ---\n%s", sb, scr)
	}
	// And the scrollback must not be dominated by blank rows (the bug: the
	// unmaterialized canvas rows are re-emitted as blank lines).
	blank := 0
	for _, row := range emu2.Scrollback() {
		if strings.TrimSpace(row) == "" {
			blank++
		}
	}
	if blank > 2 {
		t.Errorf("scrollback has %d blank rows after resize (history replaced by blanks)\n--- scrollback ---\n%s", blank, sb)
	}
}
