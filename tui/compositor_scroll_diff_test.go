// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// TestCompositor_ScrollDiff_PhysicalBaseline replays the exact /quota + /skill
// corruption from term.log: an overlay frame bakes popup rows into prevLines,
// then the popup closes and a bottom-append scrolls the window while the rows
// BEHIND the popup change identity. repaintWindow's row baseline must be the
// row the terminal PHYSICALLY shows after the scroll — not a stale
// screen-position guess — or partial splices keep prefix cells of the popup
// (the "──┌────" / "› /sID │ Title" artifacts) and the unchanged-row skip
// leaves whole stale rows.
func TestCompositor_ScrollDiff_PhysicalBaseline(t *testing.T) {
	const w, h, chromeH = 60, 20, 2
	const transcriptLen = 30
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)

	row := func(tag string, i int) string {
		s := fmt.Sprintf("%s%02d ", tag, i)
		return s + strings.Repeat("-", w-1-len(s)) + "|"
	}
	// chrome places the 2-row chrome band at the canvas bottom for a
	// transcript of n rows (mirrors how the scene builder anchors chrome).
	chrome := func(n int, ed string) []Layer {
		return []Layer{
			{Name: "ed", Kind: LayerBase, Rect: Rect{X: 0, Y: n, W: w, H: 1}, Content: []string{ed}},
			{Name: "st", Kind: LayerBase, Rect: Rect{X: 0, Y: n + 1, W: w, H: 1}, Content: []string{"STATUS"}},
		}
	}

	// Frame 1: 30 transcript rows + 2 chrome rows. vt = 32-20 = 12.
	base := make([]string, transcriptLen)
	for i := range base {
		base[i] = row("T", i)
	}
	scene1 := &Scene{TerminalW: w, TerminalH: h, ChromeHeight: chromeH,
		Layers: append([]Layer{
			{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: w, H: transcriptLen}, Content: base},
		}, chrome(transcriptLen, "> ")...),
	}
	comp.Render(scene1)

	// Frame 2: autocomplete popup overlay at viewport rows 8..11 -> canvas
	// indices 20..23 (vt=12). Overlay forces a full repaint with the popup
	// baked into prevLines.
	popup := []string{
		"-- Modifiers " + strings.Repeat("-", w-2-len("-- Modifiers ")) + "|",
		"  /skill:run:dream  Internal skill" + strings.Repeat(" ", w-2-len("  /skill:run:dream  Internal skill")) + "|",
		"  /skill:run:telegram  Telegraphic" + strings.Repeat(" ", w-2-len("  /skill:run:telegram  Telegraphic")) + "|",
		"  /quota:resets  Show resets" + strings.Repeat(" ", w-2-len("  /quota:resets  Show resets")) + "|",
	}
	scene2 := &Scene{TerminalW: w, TerminalH: h, ChromeHeight: chromeH,
		Layers: append([]Layer{
			{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: w, H: transcriptLen}, Content: base},
			{Name: "popup", Kind: LayerOverlay, Z: 1, Rect: Rect{X: 0, Y: 8, W: w, H: 4}, Content: popup},
		}, chrome(transcriptLen, "> /sk")...),
	}
	comp.Render(scene2)

	// Frame 3: popup closed; 5 rows appended at the bottom (the /skill
	// response) => scroll n=5; AND every transcript row is replaced in place
	// (the /quota:resets table landed). The new rows at the popup's old
	// screen rows share a prefix with the popup text, so planPartialRow
	// emits a column-range splice — corrupt unless the baseline matches the
	// physical screen.
	cur := make([]string, transcriptLen+5)
	for i := 0; i < transcriptLen+5; i++ {
		cur[i] = row("Q", i)
	}
	// Popup-covered canvas indices were 20..23; after the +5 scroll those
	// SCREEN rows show canvas[25..28]. Give them prefixes matching the popup
	// rows so a wrong baseline yields a partial splice instead of a full-row
	// rewrite (the term.log frame-25 condition).
	cur[25] = "-- " + strings.Repeat("=", w-1-3) + "|" // shares "-- " with popup row 0
	cur[26] = "  /skill:run:dream  TABLE" + strings.Repeat(" ", w-1-len("  /skill:run:dream  TABLE")) + "|"
	cur[27] = row("Q", 27)
	cur[28] = row("Q", 28)
	scene3 := &Scene{TerminalW: w, TerminalH: h, ChromeHeight: chromeH,
		Layers: append([]Layer{
			{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: w, H: transcriptLen + 5}, Content: cur},
		}, chrome(transcriptLen+5, "> ")...),
	}
	comp.Render(scene3)

	// Replay every frame through the cell-accurate emulator and compare each
	// visible row against the expected canvas row (vt3 = 37-20 = 17). The
	// cell emulator is required here: the string-based screenEmulator cannot
	// model partial column splices (it replaces/appends whole rows), so it
	// would report false corruption on the very head/tail splices under test.
	emu := NewTermEmulator(h, w)
	for _, wr := range term.writes {
		emu.Process(wr)
	}
	const vt3 = transcriptLen + 5 + chromeH - h // 17
	for sr := 0; sr < h-chromeH; sr++ {
		got := padOrTrim(ansiClean(emu.Visible(sr)), w)
		want := cur[vt3+sr]
		if got != want {
			t.Errorf("screen row %d corrupted:\n got %q\nwant %q", sr+1, got, want)
		}
	}
	if got := padOrTrim(ansiClean(emu.Visible(h-chromeH)), w); got != padOrTrim("> ", w) {
		t.Errorf("editor row corrupted: got %q", got)
	}
}

// TestCompositor_ScrollDiff_InPlaceEditNoScrollStillWorks guards the benign
// regime: without a scroll the screen-position baseline IS the physical row,
// so in-place edits repaint exactly.
func TestCompositor_ScrollDiff_InPlaceEditNoScrollStillWorks(t *testing.T) {
	const w, h, chromeH = 40, 10, 2
	const transcriptLen = 20
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)

	mk := func(tag string, n int, ed string) *Scene {
		rows := make([]string, n)
		for i := range rows {
			rows[i] = fmt.Sprintf("%s%02d %-34s|", tag, i, "")
		}
		return &Scene{TerminalW: w, TerminalH: h, ChromeHeight: chromeH,
			Layers: []Layer{
				{Name: "chat", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: w, H: n}, Content: rows},
				{Name: "ed", Kind: LayerBase, Rect: Rect{X: 0, Y: n, W: w, H: 1}, Content: []string{ed}},
				{Name: "st", Kind: LayerBase, Rect: Rect{X: 0, Y: n + 1, W: w, H: 1}, Content: []string{"STATUS"}},
			},
		}
	}
	comp.Render(mk("T", transcriptLen, "> "))
	// Same length (no scroll): edit one window row in place.
	scene := mk("T", transcriptLen, "> x")
	scene.Layers[0].Content[15] = "EDITED" + strings.Repeat("-", w-1-6) + "|"
	comp.Render(scene)

	emu := NewTermEmulator(h, w)
	for _, wr := range term.writes {
		emu.Process(wr)
	}
	const vt = transcriptLen + chromeH - h // 12
	want := "EDITED" + strings.Repeat("-", w-1-6) + "|"
	if got := padOrTrim(ansiClean(emu.Visible(15-vt)), w); got != want {
		t.Errorf("in-place edit not repainted: got %q want %q", got, want)
	}
}

func padOrTrim(s string, w int) string {
	if len(s) > w {
		return s[:w]
	}
	return s + strings.Repeat(" ", w-len(s))
}
