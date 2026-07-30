// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// dipScene builds a one-layer scene of transcript rows tr plus chromeH
// "CHROME" rows, mimicking the chat transcript + pinned bottom chrome.
func dipScene(w, h, chromeH int, tr []string) *Scene {
	content := append([]string{}, tr...)
	for i := 0; i < chromeH; i++ {
		content = append(content, "CHROME")
	}
	return &Scene{TerminalW: w, TerminalH: h, ChromeHeight: chromeH,
		Layers: []Layer{{Name: "c", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: w, H: len(content)}, Content: content}}}
}

// countExactRows counts rows in hay whose trimmed content equals want.
func countExactRows(hay []string, want string) int {
	n := 0
	for _, r := range hay {
		if strings.TrimSpace(r) == want {
			n++
		}
	}
	return n
}

// TestCompositor_DipRegrowAcrossWatermark_NoCorruption reproduces the
// streaming stutter reported against goal-mode runs: identical streamed lines
// appearing multiple times in the terminal (scrollback duplication) while the
// assistant message streams below a live tool widget.
//
// Mechanism: a mid-stream canvas shrink (tool widget finalizing, thinking
// block collapsing, stream-retry retracting the partial bubble) dips the
// natural viewport top below the scrollback watermark (vt < scrollTop) — the
// regime windowTop explicitly permits. While dipped, no scroll emission
// happens. But when the canvas then regrows PAST the watermark within a
// single frame (a large streamed delta), advanceScrollback computes
// from = max(c.vt, scrollTop) = scrollTop while the PHYSICAL screen top still
// shows canvas row c.vt (< scrollTop). emitSteadyScroll's line-feeds push the
// physical top rows — already emitted into scrollback — in a second time
// (DUPLICATES), while the watermark advances past rows that never physically
// scrolled (LOST rows).
//
// The invariant is the one from TestCompositor_ChromeChangeDoesNotDuplicate
// Scrollback: no row lost, no row duplicated within scrollback, no row
// duplicated within the visible window.
func TestCompositor_DipRegrowAcrossWatermark_NoCorruption(t *testing.T) {
	const w, h, chromeH = 40, 10, 2 // transcript window = 8 rows
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)

	rows := make([]string, 44)
	for i := range rows {
		rows[i] = "row-" + itoaStr(i)
	}

	// Phase 1: stream one row per frame to a steady scrolled state.
	// len=42, vt=32, scrollTop=32; screen shows transcript[32..39] + chrome.
	for n := 1; n <= 40; n++ {
		comp.Render(dipScene(w, h, chromeH, rows[:n]))
	}

	// Phase 2 (dip): a tool widget INSIDE the window finalizes — transcript
	// rows [34..38) collapse into a single "widget-done" row. The canvas
	// shrinks by 3: len=39, natural vt=29 < scrollTop=32. No scroll emission
	// this frame (watermark never moves backward); repaintWindow shows the
	// dipped window (the sanctioned border overlap).
	dipped := append([]string{}, rows[:34]...)
	dipped = append(dipped, "widget-done")
	dipped = append(dipped, rows[38:40]...) // 37 transcript rows
	comp.Render(dipScene(w, h, chromeH, dipped))

	// Phase 3 (regrow past the watermark in ONE frame): a large streamed
	// delta appends 4 rows. len=43, vt=33 > scrollTop=32. The scroll
	// emission must push exactly transcript row 32 ("row-32") into
	// scrollback — not rows 29..31 a second time.
	regrown := append(append([]string{}, dipped...), rows[40:44]...)
	comp.Render(dipScene(w, h, chromeH, regrown))

	// Replay every emitted byte through the faithful emulator.
	emu := NewTermEmulator(h, w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}
	scrollback := emu.Scrollback()
	var screen []string
	for r := 0; r < h; r++ {
		screen = append(screen, strings.TrimSpace(emu.Visible(r)))
	}
	dump := "\n--- screen ---\n" + strings.Join(screen, "\n") + "\n--- scrollback ---\n" + strings.Join(scrollback, "\n")

	// Expected final transcript identities: row-00..row-33, widget-done,
	// row-38..row-43.
	expected := append([]string{}, rows[:34]...)
	expected = append(expected, "widget-done")
	expected = append(expected, rows[38:44]...)
	for _, want := range expected {
		inSB := countExactRows(scrollback, want)
		onScreen := countExactRows(screen, want)
		switch {
		case inSB+onScreen == 0:
			t.Errorf("transcript row %q LOST from the terminal%s", want, dump)
		case inSB > 1:
			t.Errorf("transcript row %q DUPLICATED within scrollback (%d times) — stutter%s", want, inSB, dump)
		case onScreen > 1:
			t.Errorf("transcript row %q duplicated within the visible window (%d times)%s", want, onScreen, dump)
		}
	}
}

// TestCompositor_TickingWidgetInScrollOffRegion_NoResetStorm reproduces the
// reset storm: a LIVE tool widget (elapsed-time ticker) sitting in the
// scroll-off region changes its row bytes on every animation tick while the
// transcript scrolls. scrollOffUnstable treats the benign same-position text
// change as a malignant mid-transcript edit and routes the frame through
// drawWindowResetScrollback — wiping scrollback (\x1b[3J) and re-emitting the
// ENTIRE transcript per tick. On terminals honoring \x1b[3J that is an
// O(transcript) rewrite per frame; on terminals that don't (some
// multiplexers), it appends the whole transcript into scrollback again —
// massive duplication.
//
// A same-position text edit must be benign for the incremental scroll: the
// physical row that scrolls off carries the right identity (one tick stale),
// which is fine for scrollback. The test asserts NO scrollback wipe
// (\x1b[3J) occurs while a ticking widget crosses the scroll-off region.
func TestCompositor_TickingWidgetInScrollOffRegion_NoResetStorm(t *testing.T) {
	const w, h = 40, 10 // no chrome: full-screen transcript region
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)

	// Stream one row per frame; the transcript scrolls past the widget. The
	// widget text changes at the SAME positions every frame — a benign edit.
	for n := 1; n <= 30; n++ {
		comp.Render(&Scene{TerminalW: w, TerminalH: h,
			Layers: []Layer{{Name: "c", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: w, H: n}, Content: tickingRows(n, n)}}})
	}

	wipes := 0
	for _, wr := range term.Writes() {
		if strings.Contains(wr, "\x1b[3J") {
			wipes++
		}
	}
	// The only legitimate wipe is none at all: no width change, no Clear, no
	// first frame with scrollback. Every \x1b[3J here is a reset-storm frame.
	if wipes != 0 {
		t.Errorf("scrollback wiped %d times during steady scrolling with a ticking widget (reset storm)", wipes)
	}

	// And the terminal must still hold every stable row exactly once across
	// scrollback + screen.
	emu := NewTermEmulator(h, w)
	for _, wr := range term.Writes() {
		emu.Process(wr)
	}
	assertStableRowsOnce(t, emu, h)
}

// tickingWidgetAt is the transcript position of the simulated live widget in
// the reset-storm repro.
const tickingWidgetAt = 12

// tickingRows builds n transcript rows with a 3-row widget at
// tickingWidgetAt whose text ticks every frame (same positions, new bytes).
func tickingRows(n, tick int) []string {
	tr := make([]string, n)
	for i := range tr {
		tr[i] = "row-" + itoaStr(i)
	}
	widget := []string{"WIDGET elapsed " + itoaStr(tick) + "s", "WIDGET out " + itoaStr(tick), "WIDGET tail"}
	for j, line := range widget {
		if tickingWidgetAt+j < n {
			tr[tickingWidgetAt+j] = line
		}
	}
	return tr
}

// assertStableRowsOnce asserts that every non-ticking transcript row appears
// at least once across scrollback+screen and never twice within scrollback.
func assertStableRowsOnce(t *testing.T, emu *TermEmulator, h int) {
	t.Helper()
	scrollback := emu.Scrollback()
	var screen []string
	for r := 0; r < h; r++ {
		screen = append(screen, strings.TrimSpace(emu.Visible(r)))
	}
	for i, want := range tickingRows(30, 29) {
		if i == tickingWidgetAt || i == tickingWidgetAt+1 {
			continue // ticking text changes identity per frame by construction
		}
		inSB := countExactRows(scrollback, want)
		onScreen := countExactRows(screen, want)
		if inSB+onScreen == 0 {
			t.Errorf("row %q LOST (scrollback=%d screen=%d)", want, inSB, onScreen)
		} else if inSB > 1 {
			t.Errorf("row %q DUPLICATED in scrollback (%d times)", want, inSB)
		}
	}
}
