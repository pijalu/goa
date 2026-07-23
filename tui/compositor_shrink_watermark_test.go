// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
)

// TestCompositor_ShrinkBelowWatermarkDoesNotRedrawScrollbackRows is the
// regression test for the mascot-redraw bug captured in term.log
// (2026-07-23 07:52:39.519): during a tool call the chat transcript
// transiently collapsed (thinking-block finalization) so the canvas height
// dropped from >TerminalH to <=TerminalH for ONE frame. vt = len(canvas)-H
// fell to 0 while the scrollback watermark (scrollTop) was already 34, so the
// diff repaint redrew canvas rows [0, …) — the header/mascot — onto the
// visible screen even though those rows were already in terminal scrollback.
//
// Invariant: once a canvas row has been emitted into terminal scrollback
// (row index < scrollTop), it must NEVER be painted into the visible window
// again. The terminal cannot "unscroll" it; repainting it duplicates the row
// on screen. When the canvas shrinks below the watermark the compositor must
// keep the window anchored at scrollTop (the rows the terminal actually shows
// there) rather than following vt below the watermark.
func TestCompositor_ShrinkBelowWatermarkDoesNotRedrawScrollbackRows(t *testing.T) {
	term := &fakeTerminal{w: 40, h: 10}
	comp := NewCompositor(term)

	mascot := []string{"MASCOT-1", "MASCOT-2", "MASCOT-3"}
	chat := func(n int) []string {
		c := make([]string, n)
		for i := range c {
			c[i] = "chat line " + itoaStr(i)
		}
		return c
	}
	// noChrome scene: header (3) + chat (n). height=10.
	scene := func(n int) *Scene {
		cc := chat(n)
		return &Scene{
			TerminalW: 40, TerminalH: 10,
			Layers: []Layer{
				{Name: "header", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 40, H: len(mascot)}, Content: mascot},
				{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: len(mascot), W: 40, H: len(cc)}, Content: cc},
			},
		}
	}

	comp.Render(scene(30)) // scrolled: header + early chat in scrollback, scrollTop=30+3-10=23
	emu := newScreenEmulator(10, 40)
	for _, w := range term.writes {
		emu.Process(w)
	}
	if visibleContains(emu, 10, "MASCOT") {
		t.Fatalf("precondition: mascot should be scrolled off:\n%s", dumpEmu(emu, 10))
	}

	// Transient collapse: chat shrinks so the whole canvas fits on screen
	// (vt would be 0 < scrollTop=23). The mascot must NOT pop back on screen.
	writesBefore := len(term.writes)
	comp.Render(scene(5)) // canvas = 3+5 = 8 <= 10 → vt=0

	for i := writesBefore; i < len(term.writes); i++ {
		if strings.Contains(term.writes[i], "MASCOT") {
			t.Errorf("write[%d] during shrink-below-watermark redrew scrollback mascot onto visible screen: %q",
				i, truncEscape(term.writes[i]))
		}
	}

	// And the visible screen must still not contain the mascot.
	emu2 := newScreenEmulator(10, 40)
	for _, w := range term.writes {
		emu2.Process(w)
	}
	if visibleContains(emu2, 10, "MASCOT") {
		t.Errorf("after shrink-below-watermark the mascot is visible on screen:\n%s", dumpEmu(emu2, 10))
	}
}
