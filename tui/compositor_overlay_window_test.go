// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestOverlayPlacementSharesWindowMapping is the regression test for the
// autocomplete-popup corruption: typing "/goal:r" left the completion items
// split into two bands with blank rows between them and stale rows around the
// editor.
//
// Root cause: overlay layers (the popup is one) are positioned in SCREEN rows
// (Rect.Y == 0 is the top of the visible window), but placeOverlays translated
// them into canvas rows with the bottom-anchored viewport top
// (baseHeight - termH) while the painter used its own — watermark-clamped —
// window top. The two only agree while the scrollback watermark equals the
// bottom anchor. The moment the canvas SHRINKS (the goal bubble clearing while
// the popup was open took 3 rows out of the chrome band) the watermark sits
// BELOW the natural anchor, so every overlay row landed 3 canvas rows too low:
// part of the popup was painted onto canvas rows the painter drops as blank
// filler and part onto the pinned chrome band, leaving the observed gaps.
//
// The fix is one shared windowMapping: the painter, Scene.AgentFrame and
// overlay placement all derive their rows from it. This test pins the
// user-visible contract that the mapping serves — a popup row declared at
// screen row Y is painted at screen row Y, every screen row shows exactly the
// canvas row the mapping assigns to it, and rows are never left stale.
func TestOverlayPlacementSharesWindowMapping(t *testing.T) {
	const (
		w           = 80
		h           = 20
		transcriptH = 20 // transcript rows (scrolled: canvas > terminal)
		chromeWide  = 5  // separator + 3 goal-bubble rows + editor
		chromeNarrow = 2 // separator + editor (the goal bubble clears)
		popupY      = 10 // screen row the popup is anchored at
	)
	popup := []string{"POPUP-0", "POPUP-1", "POPUP-2"}

	transcript := make([]string, transcriptH)
	for i := range transcript {
		transcript[i] = fmt.Sprintf("line-%02d", i)
	}
	wideChrome := []string{"--- sep ---", "goal title", "goal body", "goal foot", "editor"}
	narrowChrome := []string{"--- sep ---", "editor"}

	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)
	emu := NewTermEmulator(h, w)
	feed := func() {
		for _, frame := range term.Writes() {
			emu.Process(frame)
		}
		term.mu.Lock()
		term.writes = nil
		term.mu.Unlock()
	}

	// Frame 1: a transcript taller than the screen with a wide chrome band. The
	// top rows scroll into scrollback and the watermark advances past them.
	wide := mappingScene(w, h, transcript, wideChrome, chromeWide, nil)
	comp.Render(wide)
	feed()
	requireScreenMatchesCanvas(t, emu, wide, "wide chrome")

	// Frame 2: the chrome band shrinks by three rows (the goal bubble clears)
	// while the popup is open. The canvas is now shorter than the watermark
	// implies, which is exactly the geometry that used to split the popup.
	narrow := mappingScene(w, h, transcript, narrowChrome, chromeNarrow, &Layer{
		Name:    "popup",
		Kind:    LayerOverlay,
		Rect:    Rect{X: 4, Y: popupY, W: 20, H: len(popup)},
		Content: popup,
		Z:       10,
	})
	comp.Render(narrow)
	feed()
	requireScreenMatchesCanvas(t, emu, narrow, "narrow chrome")

	// The popup's rows must appear at the screen rows it declared, contiguous
	// and with no blank row between them.
	for i, want := range popup {
		got := strings.TrimSpace(emu.Visible(popupY + i))
		if !strings.Contains(got, want) {
			t.Fatalf("popup row %d: screen row %d = %q, want it to contain %q\n%s",
				i, popupY+i, got, want, screenDump(emu))
		}
	}
}

// mappingScene builds a scene with the given transcript and chrome rows plus an
// optional overlay. The chrome band is the LAST chromeH rows of the canvas, as
// Scene.ChromeHeight declares to the compositor.
func mappingScene(w, termH int, transcript, chrome []string, chromeH int, overlay *Layer) *Scene {
	layers := []Layer{{
		Name:    "chat",
		Kind:    LayerBase,
		Rect:    Rect{X: 0, Y: 0, W: w, H: len(transcript)},
		Content: transcript,
	}}
	for i, line := range chrome {
		layers = append(layers, Layer{
			Name:    fmt.Sprintf("chrome-%d", i),
			Kind:    LayerBase,
			Rect:    Rect{X: 0, Y: len(transcript) + i, W: w, H: 1},
			Content: []string{line},
		})
	}
	if overlay != nil {
		layers = append(layers, *overlay)
	}
	return &Scene{
		TerminalW:    w,
		TerminalH:    termH,
		ChromeHeight: chromeH,
		Layers:       layers,
	}
}

// requireScreenMatchesCanvas asserts the terminal emulator's screen shows
// exactly the canvas rows the frame's window mapping assigns to each screen
// row: no stale rows, no shifted rows, no content in the blank filler rows.
func requireScreenMatchesCanvas(t *testing.T, emu *TermEmulator, scene *Scene, step string) {
	t.Helper()
	canvas, _ := scene.compose(0)
	m := scene.mapping(len(canvas))
	for row := 0; row < scene.TerminalH; row++ {
		want := ""
		if ci, ok := m.canvasRow(row); ok && ci < len(canvas) {
			want = strings.TrimSpace(stripANSI(canvas[ci]))
		}
		got := strings.TrimSpace(emu.Visible(row))
		if got != want {
			t.Fatalf("%s: screen row %d = %q, mapping says canvas row %s = %q\n%s",
				step, row, got, canvasRowLabel(m, row), want, screenDump(emu))
		}
	}
}

// canvasRowLabel names the canvas row behind a screen row for failure output.
func canvasRowLabel(m windowMapping, screenRow int) string {
	if ci, ok := m.canvasRow(screenRow); ok {
		return fmt.Sprintf("%d", ci)
	}
	return "(none)"
}

// screenDump renders the emulator's screen for failure output.
func screenDump(emu *TermEmulator) string {
	var b strings.Builder
	_, h := emu.Size()
	for row := 0; row < h; row++ {
		fmt.Fprintf(&b, "%2d | %s\n", row, strings.TrimRight(emu.Visible(row), " "))
	}
	return b.String()
}
