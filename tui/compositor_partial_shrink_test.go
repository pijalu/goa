// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
)

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
	all := append([]string{}, emu.Scrollback()...)
	for r := 0; r < 10; r++ {
		all = append(all, emu.Visible(r))
	}
	// No row may appear twice across scrollback+screen.
	for i := 0; i < 21; i++ {
		row := "row-" + itoaStr(i)
		count := 0
		for _, r := range all {
			if strings.TrimSpace(r) == row {
				count++
			}
		}
		if count > 1 {
			t.Errorf("row %q appears %d times (want ≤1)\nscrollback:\n%s\nscreen:\n%s",
				row, count,
				strings.Join(emu.Scrollback(), "\n"), joinVisibleEmu(emu, 10))
		}
	}
	// The newest row must still be visible (just not duplicated).
	found := false
	for r := 0; r < 10; r++ {
		if strings.Contains(emu.Visible(r), "row-19") {
			found = true
		}
	}
	if !found {
		t.Errorf("newest content missing after 1-row shrink:\n%s", joinVisibleEmu(emu, 10))
	}
}
