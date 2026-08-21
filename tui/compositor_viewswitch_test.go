// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

// viewSwitchScene builds a single-layer base scene whose transcript rows are
// the given lines (the "chrome" is a 1-row input/footer band, matching the
// real layout where ChromeHeight pins the bottom rows). The scene is stamped
// with the compositor's CURRENT clear generation, exactly as
// TUI.buildSnapshot stamps it — a view switch bumps that generation, so
// scenes composed after the switch carry the new one.
func viewSwitchScene(c *Compositor, w, h, chromeH int, lines []string, gen uint64) *Scene {
	content := make([]string, len(lines))
	copy(content, lines)
	layers := []Layer{
		{Name: "transcript", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: w, H: len(content)}, Content: content},
	}
	if chromeH > 0 {
		layers = append(layers, Layer{
			Name: "chrome", Kind: LayerBase,
			Rect:    Rect{X: 0, Y: len(content), W: w, H: chromeH},
			Content: []string{"> input"},
		})
	}
	return &Scene{TerminalW: w, TerminalH: h, ChromeHeight: chromeH, Layers: layers, MutationGen: gen, ClearGen: c.ClearGen()}
}

// emuDump joins the emulator's visible rows for assertion messages.
func emuDump(emu *screenEmulator) string {
	var b strings.Builder
	for r := 0; r < emu.h; r++ {
		b.WriteString(emu.Visible(r))
		b.WriteString("\n")
	}
	return b.String()
}

// TestCompositor_ViewSwitchRepaintOnly is the T2 switch-behavior contract at
// the protocol-owner level:
//
//   - RestoreFrame arms a full visible-window repaint of the NEW canvas via
//     the in-place per-row repaint (no full-screen wipe, no scrollback wipe).
//   - Rows the new view accumulated while inactive scroll into the terminal
//     scrollback exactly once (first-time emission, same as if it had been
//     live); already-committed rows are never RE-emitted.
//   - The previous view's rows vanish from the screen.
func TestCompositor_ViewSwitchRepaintOnly(t *testing.T) {
	// The compositor floors tiny terminals at 80x24, so use h=12 (an 11-row
	// transcript window) and a 14-row B transcript to exercise scroll-off.
	const w, h = 40, 12
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)

	// View A on screen: a few rows + the chrome band. InitialClear stands in
	// for TUI.Start's first-frame wipe.
	comp.InitialClear()
	aLines := []string{"A-one", "A-two", "A-three"}
	comp.Render(viewSwitchScene(comp, w, h, 1, aLines, 1))

	// Save A's baseline (switch away), restore B's virgin baseline.
	aSnap := comp.ExportFrame()
	if aSnap.ScrollTop != 0 {
		t.Fatalf("A fits the window: ScrollTop = %d, want 0", aSnap.ScrollTop)
	}
	comp.RestoreFrame(FrameState{}) // B was never mounted: zero baseline

	// B accumulated more rows than the transcript window (h-chromeH = 11)
	// while inactive: 14 rows, so 3 must scroll off on the switch frame.
	bLines := []string{"B-01", "B-02", "B-03", "B-04", "B-05", "B-06", "B-07", "B-08", "B-09", "B-10", "B-11", "B-12", "B-13", "B-14"}
	writesBefore := len(term.writes)
	comp.Render(viewSwitchScene(comp, w, h, 1, bLines, 1))

	// Assert the switch frame itself: no ED2/ED3 (no full wipe, no scrollback
	// wipe) — the repaint is per-row in place.
	switchWrites := strings.Join(term.writes[writesBefore:], "")
	if strings.Contains(switchWrites, "\x1b[2J") || strings.Contains(switchWrites, "\x1b[3J") {
		t.Errorf("switch frame wiped screen/scrollback; want in-place repaint only:\n%q", switchWrites)
	}

	emu := newScreenEmulator(h, w)
	for _, wr := range term.writes {
		emu.Process(wr)
	}
	screen := emuDump(emu)

	// The visible window shows B's tail (rows that fit): B-04..B-14.
	assertScreenHas(t, screen, "B-04", "B-14")
	// B's scrolled-off rows are in terminal scrollback exactly once (first
	// emission), not duplicated and not on screen; A's rows are gone (they
	// stay in whatever scrollback they earned while live — here none).
	assertScrolledOffOnce(t, emu, screen, "B-01", "B-02", "B-03")
	assertScreenLacks(t, screen, "A-one", "A-three")

	// B's baseline is now live: the watermark equals the emitted row count.
	if got := comp.ScrollWatermark(); got != 3 {
		t.Errorf("watermark after switch = %d, want 3 (B-01..B-03 in scrollback)", got)
	}

	// Switching back to A restores A's exact baseline truth.
	bSnap := comp.ExportFrame()
	if bSnap.ScrollTop != 3 {
		t.Fatalf("B ExportFrame ScrollTop = %d, want 3", bSnap.ScrollTop)
	}
	comp.RestoreFrame(aSnap)
	comp.Render(viewSwitchScene(comp, w, h, 1, aLines, 1))

	emu2 := newScreenEmulator(h, w)
	for _, wr := range term.writes {
		emu2.Process(wr)
	}
	screen2 := emuDump(emu2)
	assertScreenHas(t, screen2, "A-one", "A-three")
	assertScreenLacks(t, screen2, "B-10", "B-14")
}

// assertScreenHas asserts every marker appears on the visible screen.
func assertScreenHas(t *testing.T, screen string, markers ...string) {
	t.Helper()
	for _, m := range markers {
		if !strings.Contains(screen, m) {
			t.Errorf("screen missing %q:\n%s", m, screen)
		}
	}
}

// assertScreenLacks asserts no marker appears on the visible screen.
func assertScreenLacks(t *testing.T, screen string, markers ...string) {
	t.Helper()
	for _, m := range markers {
		if strings.Contains(screen, m) {
			t.Errorf("%q must not be on the visible screen:\n%s", m, screen)
		}
	}
}

// assertScrolledOffOnce asserts each marker sits in the terminal scrollback
// exactly once and NOT on the visible screen.
func assertScrolledOffOnce(t *testing.T, emu *screenEmulator, screen string, markers ...string) {
	t.Helper()
	joinedSB := strings.Join(emu.Scrollback(), "\n")
	for _, m := range markers {
		if n := strings.Count(joinedSB, m); n != 1 {
			t.Errorf("%q appears %d times in scrollback, want exactly 1: %v", m, n, emu.Scrollback())
		}
		if strings.Contains(screen, m) {
			t.Errorf("%q must not be on the visible screen:\n%s", m, screen)
		}
	}
}

// TestCompositor_RestoreFrameDropsStaleScene verifies the generation guard: a
// scene snapshot taken BEFORE the switch (stamped with the old clear
// generation) but rendered AFTER RestoreFrame must be dropped, not diffed
// against the restored baseline (which would repaint the old view's rows on
// top of the new one or mis-emit scrollback).
func TestCompositor_RestoreFrameDropsStaleScene(t *testing.T) {
	const w, h = 40, 12
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)
	comp.InitialClear()

	stale := viewSwitchScene(comp, w, h, 1, []string{"A-one"}, 1)
	stale.ClearGen = comp.ClearGen() // stamped at snapshot time, pre-switch

	comp.RestoreFrame(FrameState{}) // switch to B: bumps the generation
	writesBefore := len(term.writes)
	comp.Render(stale)              // stale scene arrives late
	if len(term.writes) != writesBefore {
		t.Errorf("stale pre-switch scene was rendered; want it dropped (wrote %q)",
			strings.Join(term.writes[writesBefore:], ""))
	}

	// A fresh scene (stamped with the new generation) renders normally.
	fresh := viewSwitchScene(comp, w, h, 1, []string{"B-one"}, 1)
	fresh.ClearGen = comp.ClearGen()
	comp.Render(fresh)
	emu := newScreenEmulator(h, w)
	for _, wr := range term.writes {
		emu.Process(wr)
	}
	if !visibleContains(emu, h, "B-one") {
		t.Errorf("fresh post-switch scene not rendered:\n%s", emuDump(emu))
	}
}

// TestCompositor_ExportFrameCopiesBaseline verifies the exported snapshot is
// detached from the live compositor: later frames must not mutate it.
func TestCompositor_ExportFrameCopiesBaseline(t *testing.T) {
	term := &fakeTerminal{w: 40, h: 8}
	comp := NewCompositor(term)
	comp.InitialClear()
	comp.Render(viewSwitchScene(comp, 40, 12, 1, []string{"row-a"}, 1))

	snap := comp.ExportFrame()
	if len(snap.PrevLines) == 0 {
		t.Fatal("ExportFrame should capture the rendered baseline")
	}
	first := snap.PrevLines[0]

	comp.Render(viewSwitchScene(comp, 40, 12, 1, []string{"row-a", "row-b"}, 2))
	if snap.PrevLines[0] != first || len(snap.PrevLines) != 2 { // "row-a" + chrome row
		t.Errorf("snapshot mutated by a later frame: %+v", snap)
	}
}
