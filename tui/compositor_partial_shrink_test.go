// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import "testing"

// TestCompositor_OneRowShrinkNoDuplicate verifies that a 1-row transcript
// shrink does NOT duplicate a scrolled-off row onto the screen. The watermark
// clamp (windowTop) anchors the window at scrollTop, leaving a truthful blank
// row at the bottom — the alternative (dipping below the watermark) repaints
// an already-scrolled row, showing it both in scrollback AND on screen (the
// screen-glitching bug from bugs.md).
func TestCompositor_OneRowShrinkNoDuplicate(t *testing.T) {
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
	comp.Render(scene(20)) // scrollTop advances
	comp.Render(scene(21)) // grow by one: scrollTop -> 11
	comp.Render(scene(20)) // shrink by one: natural vt=10, scrollTop=11

	emu := NewTermEmulator(10, 40)
	for _, w := range term.Writes() {
		emu.Process(w)
	}
	assertShrinkRowsUnique(t, emu)
	assertShrinkNewestVisible(t, emu)
}
