// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
)

// TestCompositor_OneRowShrinkNoBlankBottom is the regression test for the
// "screen shrank one line" regression introduced by the first watermark clamp.
//
// The scrollback-watermark clamp (windowTop) was applied uniformly: whenever
// the natural viewport top dipped below scrollTop, the window was anchored at
// the watermark. For a SMALL mid-transcript shrink (the transcript loses a row
// or two while still taller than the terminal), scrollTop stays one row too
// high, the clamped window then covers only height-1 real rows, and the bottom
// row of the screen is left blank — the terminal visibly "shrinks by one line"
// (term.log 2026-07-23 11:56, footer at rows 34-35 with an orphaned blank row
// 36).
//
// The clamp must only apply to a DEEP collapse (the whole canvas fits on
// screen, where clamping prevents the mascot flash). For a partial shrink the
// window must stay full: repaint the natural bottom anchor even if it dips a
// row or two into already-scrolled rows (imperceptible — it reads as ordinary
// scroll-back).
func TestCompositor_OneRowShrinkNoBlankBottom(t *testing.T) {
	term := &fakeTerminal{w: 40, h: 10}
	comp := NewCompositor(term)
	lines := func(n int) []string {
		c := make([]string, n)
		for i := range c {
			c[i] = "row-" + itoaStr(i)
		}
		return c
	}
	scene := func(n int) *Scene {
		cc := lines(n)
		return &Scene{TerminalW: 40, TerminalH: 10, Layers: []Layer{
			{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 40, H: len(cc)}, Content: cc},
		}}
	}
	emu := newScreenEmulator(10, 40)
	replay := func() {
		emu = newScreenEmulator(10, 40)
		for _, w := range term.writes {
			emu.Process(w)
		}
	}
	comp.Render(scene(20)) // scrollTop advances
	comp.Render(scene(21)) // grow by one: scrollTop -> 11
	comp.Render(scene(20)) // shrink by one: natural vt=10, scrollTop=11

	replay()
	t.Logf("screen after 1-row shrink:\n%s", dumpEmu(emu, 10))
	// The window must stay full: bottom row shows the newest content, not a
	// blank left by a window clamped one row too high.
	if strings.TrimSpace(emu.screen[9]) == "" {
		t.Errorf("bottom row blank after 1-row shrink (screen shrank one line):\n%s", dumpEmu(emu, 10))
	}
	if !visibleContains(emu, 10, "row-19") {
		t.Errorf("newest content missing after 1-row shrink:\n%s", dumpEmu(emu, 10))
	}
}
