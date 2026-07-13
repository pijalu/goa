// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"strings"
	"testing"
)

// TestCompositor_RegrowAfterShrinkDoesNotReEmitOffScreenContent is the
// regression test for the "mascot/logo suddenly appears" bug.
//
// Sequence: content grows past the screen (off-screen header/mascot lands in
// scrollback), then shrinks to fit on screen, then grows past the screen again.
// On the regrow the compositor used to re-enter emitFirstScroll (because
// applyFrameTracking reset firstScrollDone=false when the canvas fit), which
// re-writes the WHOLE canvas from row 0 — flashing the off-screen mascot back
// onto the visible screen and duplicating scrollback content.
//
// Once a session has scrolled, emitFirstScroll must never re-fire: scrollback
// already exists, and re-emitting off-screen content is both wrong and jarring.
func TestCompositor_RegrowAfterShrinkDoesNotReEmitOffScreenContent(t *testing.T) {
	term := &fakeTerminal{w: 40, h: 10}
	comp := NewCompositor(term)

	// A header/mascot layer stacked above a growing chat layer, mirroring the
	// real layout (Header sits at Y=0; ChatViewport below it).
	mascot := []string{"MASCOT-1", "MASCOT-2", "MASCOT-3"}
	chatLines := func(n int) []string {
		c := make([]string, n)
		for i := range c {
			c[i] = "chat line " + itoaStr(i)
		}
		return c
	}
	scene := func(n int) *Scene {
		cc := chatLines(n)
		return &Scene{
			TerminalW: 40, TerminalH: 10,
			Layers: []Layer{
				{Name: "header", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 40, H: len(mascot)}, Content: mascot},
				{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: len(mascot), W: 40, H: len(cc)}, Content: cc},
			},
		}
	}

	comp.Render(scene(8))  // fits on screen
	comp.Render(scene(30)) // scrolled: header + early chat now in scrollback

	// Precondition: the mascot must be off the visible screen by now.
	emu := newScreenEmulator(10, 40)
	for _, w := range term.writes {
		emu.Process(w)
	}
	if visibleContains(emu, 10, "MASCOT") {
		t.Fatalf("precondition: mascot should be scrolled off screen:\n%s", dumpEmu(emu, 10))
	}

	// Shrink to fit (e.g. a large thinking block collapsed), then regrow.
	comp.Render(scene(6)) // shrink to fit on screen (mascot correctly visible again)
	writesBeforeRegrow := len(term.writes)
	comp.Render(scene(35)) // regrow well past the screen — mascot must stay off-screen

	// None of the writes produced by the regrow may re-emit the off-screen
	// mascot/logo onto the visible screen.
	for i := writesBeforeRegrow; i < len(term.writes); i++ {
		if strings.Contains(term.writes[i], "MASCOT") {
			t.Errorf("write[%d] during regrow re-emitted off-screen mascot/logo (would flash it back on screen): %q",
				i, truncEscape(term.writes[i]))
		}
	}
}

// truncEscape returns a short, escape-flattened preview of s for error output.
func truncEscape(s string) string {
	const max = 160
	out := strings.ReplaceAll(s, "\x1b", "\\e")
	if len(out) > max {
		out = out[:max] + "..."
	}
	return out
}
