// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestCompositor_HeightBlipDoesNotDuplicateScrollback is the bugs.md item I
// regression: "1st line of history/scroll is often a duplicated line". The
// user's transcript showed a completed tool widget's header row emitted twice
// at the top of the scroll region. The trigger is a one-frame terminal-height
// blip (transient TIOCGWINSZ misread, same family as the mascot redraw H):
// the blip frame takes drawWindow, and the recovery frame can re-emit the top
// transcript row into scrollback.
//
// This drives a full viewport through a height blip (shrink by several rows
// for one frame, then restore) and asserts no transcript row appears twice in
// the resulting scrollback. With the Size()-level transient filter (terminal.go)
// the blip never reaches the compositor; this test pins the compositor's own
// watermark invariant so a blip from any other source also cannot duplicate.
func TestCompositor_HeightBlipDoesNotDuplicateScrollback(t *testing.T) {
	term := &fakeTerminal{w: 40, h: 10}
	comp := NewCompositor(term)
	scene := func(lines []string, h int) *Scene {
		return &Scene{
			TerminalW: 40, TerminalH: h,
			Layers: []Layer{{Name: "c", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: 40, H: len(lines)}, Content: lines}},
		}
	}

	// Build a transcript taller than the window so rows are in scrollback.
	var content []string
	for i := 0; i < 20; i++ {
		content = append(content, fmt.Sprintf("ROW-%02d", i))
	}
	comp.Render(scene(content, 10))

	// One-frame height blip: shrink to 7 rows for a single frame, then
	// restore to 10. drawWindow runs on both frames.
	comp.Render(scene(content, 7))
	comp.Render(scene(content, 10))

	emu := newScreenEmulator(10, 40)
	for _, w := range term.writes {
		emu.Process(w)
	}
	sb := emu.Scrollback()

	// No ROW-XX line may appear more than once across scrollback.
	for i := 0; i < 20; i++ {
		want := fmt.Sprintf("ROW-%02d", i)
		count := 0
		for _, line := range sb {
			if strings.Contains(line, want) {
				count++
			}
		}
		if count > 1 {
			t.Errorf("scrollback row %q duplicated %d times after height blip:\n%s", want, count, strings.Join(sb, "\n"))
		}
	}
}
