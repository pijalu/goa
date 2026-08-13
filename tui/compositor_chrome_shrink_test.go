// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestCompositor_ChromeShrinkNoDuplicate is the regression test for the
// bugs.md "Screen glitching" item: when the chrome band shrinks (goal bubble
// clears), the canvas shortens by the chrome delta, and the old windowTop
// returned a vt BELOW scrollTop — revealing already-scrolled rows at the top
// of the visible window. Those rows were already emitted into scrollback, so
// they appeared TWICE: once in scrollback, once on screen.
//
// The fix clamps windowTop to scrollTop unconditionally and repaints the
// window in two phases (transcript + pinned chrome), so a chrome shrink never
// pulls scrolled-off rows back onto the screen.
func TestCompositor_ChromeShrinkNoDuplicate(t *testing.T) {
	const (
		w     = 40
		termH = 10
	)
	term := &fakeTerminal{w: w, h: termH}
	comp := NewCompositor(term)

	// Build a scrolled transcript with chrome=2.
	var transcript []string
	for i := 0; i < 20; i++ {
		transcript = append(transcript, fmt.Sprintf("row-%02d", i))
	}
	mkScene := func(tr []string, chromeH int) *Scene {
		content := append([]string{}, tr...)
		for i := 0; i < chromeH; i++ {
			content = append(content, "CHROME")
		}
		return &Scene{TerminalW: w, TerminalH: termH, ChromeHeight: chromeH,
			Layers: []Layer{{Name: "c", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: w, H: len(content)}, Content: content}}}
	}

	comp.Render(mkScene(transcript, 2))

	// Grow the transcript so it scrolls.
	for i := 20; i < 30; i++ {
		transcript = append(transcript, fmt.Sprintf("row-%02d", i))
		comp.Render(mkScene(transcript, 2))
	}

	// Chrome band GROWS (goal bubble appears) then SHRINKS (clears).
	comp.Render(mkScene(transcript, 5))
	comp.Render(mkScene(transcript, 2))

	// Replay the byte stream and verify no row is duplicated.
	emu := NewTermEmulator(termH, w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}
	all := append([]string{}, emu.Scrollback()...)
	for r := 0; r < termH; r++ {
		all = append(all, emu.Visible(r))
	}
	count := func(want string) int {
		n := 0
		for _, r := range all {
			if strings.TrimSpace(r) == want {
				n++
			}
		}
		return n
	}
	dump := "\n--- scrollback ---\n" + strings.Join(emu.Scrollback(), "\n") +
		"\n--- screen ---\n" + joinVisibleEmu(emu, termH)
	for i := 0; i < len(transcript); i++ {
		row := fmt.Sprintf("row-%02d", i)
		if c := count(row); c != 1 {
			t.Errorf("row %q appears %d times (want exactly 1)%s", row, c, dump)
		}
	}
}

func joinVisibleEmu(emu *TermEmulator, h int) string {
	var rows []string
	for r := 0; r < h; r++ {
		rows = append(rows, emu.Visible(r))
	}
	return strings.Join(rows, "\n")
}
