// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"testing"
)

// Filmstrip-based visual-equality gate for column-range dirty-row emission.
//
// Each scenario drives the compositor through a sequence of scenes, replays
// every frame's bytes into a shared TermEmulator, and asserts — after EVERY
// frame — that the emulated screen is cell-for-cell identical (text AND
// per-cell fg/bg SGR) to a reference screen produced by full-row repaint of
// the same canvas. The legacy full-row emitter provably produced exactly that
// reference (it rewrote every changed row in full under a default pen), so
// equality against it proves the optimized emission renders frames visually
// identical to before — INCLUDING COLORS: the original text-only gate was
// blind to the cut-point SGR-state loss that made streamed headings, tool
// backgrounds, and borders render partially default.
// AgentFrames are captured into a Filmstrip so the structured view records
// the same evolution.

const (
	fsW = 60
	fsH = 24
)

// fsReference renders the ground-truth screen for a composed canvas the way
// the legacy full-row emitter did: every visible row rewritten in full (CUP +
// EL + content) into a fresh emulator, under the two-phase layout (transcript
// anchored at the natural viewport top, chrome band pinned at the bottom).
// Every row write starts and ends at a default pen, so cell attributes come
// solely from each row's own SGR runs — the definition of correct coloring.
func fsReference(canvas []string, w, h, chrome int) *TermEmulator {
	contentEnd := len(canvas) - chrome
	if contentEnd < 0 {
		contentEnd = 0
	}
	windowH := h - chrome
	if windowH < 1 {
		windowH = 1
	}
	vt := max(0, len(canvas)-h)
	ref := NewTermEmulator(h, w)
	var buf strings.Builder
	for sr := 1; sr <= windowH; sr++ {
		i := vt + sr - 1
		if i >= 0 && i < contentEnd {
			fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[2K%s", sr, truncateToWidth(canvas[i], w, ""))
		}
	}
	for sr := windowH + 1; sr <= h; sr++ {
		i := contentEnd + (sr - windowH - 1)
		if i >= 0 && i < len(canvas) {
			fmt.Fprintf(&buf, "\x1b[%d;1H\x1b[2K%s", sr, truncateToWidth(canvas[i], w, ""))
		}
	}
	ref.Process(buf.String())
	return ref
}

// fsAssertScreen compares the compositor-driven emulator against the
// full-repaint reference on visible text and per-cell fg/bg attributes.
func fsAssertScreen(t *testing.T, label string, step int, emu, ref *TermEmulator) {
	t.Helper()
	const h = fsH
	for r := 0; r < h; r++ {
		if got, want := emu.Visible(r), ref.Visible(r); got != want {
			t.Fatalf("%s step %d row %d text mismatch:\n got: %q\nwant: %q", label, step, r+1, got, want)
		}
		fsAssertAttrs(t, label, step, r, "fg", emu.VisibleFg(r), ref.VisibleFg(r))
		fsAssertAttrs(t, label, step, r, "bg", emu.VisibleBg(r), ref.VisibleBg(r))
	}
}

// fsAssertAttrs compares one attribute row, reporting the first divergent
// column with enough context to pinpoint the emission that lost it.
func fsAssertAttrs(t *testing.T, label string, step, row int, kind string, got, want []string) {
	t.Helper()
	for c := range want {
		if got[c] != want[c] {
			t.Fatalf("%s step %d row %d col %d %s mismatch: got %q, want %q (row text %q)",
				label, step, row+1, c+1, kind, got[c], want[c], strings.Join(got, "|"))
		}
	}
}

// fsScene wraps canvas rows into a single-layer scene with the given chrome.
func fsScene(rows []string, chrome int) *Scene {
	h := fsH
	if len(rows) > h {
		h = len(rows) // let compose materialize everything; Render clamps back
	}
	return &Scene{TerminalW: fsW, TerminalH: h, ChromeHeight: chrome,
		Layers: []Layer{{Name: "c", Kind: LayerBase, Rect: Rect{X: 0, Y: 0, W: fsW, H: len(rows)}, Content: rows}}}
}

// fsRun drives the scenes through a fresh compositor, replaying each frame's
// bytes into a shared emulator and asserting per-frame visual equality with
// the canvas-derived expectation. Returns the captured Filmstrip.
func fsRun(t *testing.T, label string, scenes []*Scene) *Filmstrip {
	t.Helper()
	const w, h = fsW, fsH
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)
	emu := NewTermEmulator(h, w)
	film := NewFilmstrip()

	for step, sc := range scenes {
		before := len(term.Writes())
		comp.Render(sc)
		fresh := term.Writes()[before:]
		for _, wr := range fresh {
			emu.Process(wr)
		}
		canvas, _ := sc.compose(0)
		fsAssertScreen(t, label, step, emu, fsReference(canvas, w, h, sc.ChromeHeight))
		film.Capture(fmt.Sprintf("%s/%d", label, step), sc.AgentFrame(h), "")
	}
	if len(film.Frames()) != len(scenes) {
		t.Fatalf("%s: captured %d frames, want %d", label, len(film.Frames()), len(scenes))
	}
	return film
}

func fsClone(rows []string) []string {
	out := make([]string, len(rows))
	copy(out, rows)
	return out
}

// TestFilmstrip_StreamingTurnVisualEquality streams an assistant reply word by
// word (in-place tail churn on the streaming transcript row), sliding the
// oldest history row out on each wrap commit. The canvas keeps a constant
// length so the chrome-band boundary never crosses a transcript row — that
// boundary drift is a known pre-existing quirk of the screen-relative skip,
// independent of column-range emission. The run exercises partial tail
// emission, position-shift full repaints, and the pinned band in one pass.
// The streaming row uses TRUECOLOR styling (38;2;…): TermEmulator only models
// extended colors, so this is what makes the per-cell fg/bg comparison able
// to catch cut-point SGR loss.
func TestFilmstrip_StreamingTurnVisualEquality(t *testing.T) {
	rows := make([]string, 17)
	for i := range rows {
		rows[i] = fmt.Sprintf("history-%02d some earlier transcript line", i)
	}
	band := []string{"╭─editor──────────────────────────────╮", "╰─status──────────────────────────────╯"}

	var scenes []*Scene
	line := ""
	words := []string{"Certainly", "here", "is", "the", "summary", "of", "the",
		"requested", "change", "with", "all", "relevant", "details", "included"}
	for k, wd := range words {
		line += wd + " "
		cur := fsClone(rows)
		cur[len(cur)-1] = "\x1b[38;2;88;166;255m" + line + "\x1b[0m"
		scenes = append(scenes, fsScene(append(cur, band...), 2))
		if (k+1)%4 == 0 { // commit: slide history up, open a fresh stream row
			rows = append(fsClone(cur)[1:], "")
		}
	}

	film := fsRun(t, "streaming", scenes)
	// The Filmstrip must record the streamed words arriving on the streaming
	// row (compose clips canvas rows to the terminal width, so assert on
	// words inside the first 60 columns).
	last := film.Last()
	found := 0
	for _, l := range last.Frame.Visible {
		if strings.Contains(l, "Certainly") || strings.Contains(l, "summary") {
			found++
		}
	}
	if found == 0 {
		t.Fatalf("filmstrip final frame missing streamed text:\n%s", film.Render())
	}
}

// TestFilmstrip_ColorVisualEquality reproduces, at compositor level, the four
// color-corruption shapes captured in term.log 2026-08-23 (export
// goa-export-20260823-140941.zip): a streamed Markdown heading whose appended
// text rendered default instead of heading blue, a tool widget line whose
// repaint during scroll dropped its green background, a tree row whose
// appended cells lost the connector fg, and an input top border whose title
// change made the rewritten half render default. Every styled span uses
// TRUECOLOR SGRs so the per-cell attribute comparison actually sees them.
// Frames grow the heading character by character, flip the tool row between
// pending and settled forms (mid-row churn under an active background), and
// scroll old rows out — the exact regime where the cut-point SGR state was
// lost.
func TestFilmstrip_ColorVisualEquality(t *testing.T) {
	const (
		headStyle  = "\x1b[1;38;2;88;166;255m"  // bold heading blue
		toolBg     = "\x1b[48;2;42;50;41m"       // tool widget green background
		toolFg     = "\x1b[38;2;139;148;158m"    // tool output gray
		treeFg     = "\x1b[38;2;139;148;158m"    // arch-tree connector gray
		borderFg   = "\x1b[38;2;48;54;61m"       // editor border slate
	)
	band := []string{borderFg + "╭─ gpt-5 " + strings.Repeat("─", 42) + "╮" + "\x1b[0m",
		borderFg + "╰" + strings.Repeat("─", 51) + "╯" + "\x1b[0m"}

	hist := func() []string {
		rows := make([]string, 16)
		for i := range rows {
			rows[i] = fmt.Sprintf("history-%02d an earlier settled line", i)
		}
		return rows
	}
	toolRow := func(pending bool) string {
		if pending {
			return toolBg + " " + toolFg + "git log --oneline -3" + "\x1b[39m  " + "\x1b[0m"
		}
		return toolBg + " " + toolFg + "Took 0.04s" + "\x1b[39m          " + "\x1b[0m"
	}

	var scenes []*Scene
	heading := ""
	for k, ch := range "# Goa — Project Summary" {
		heading += string(ch)
		rows := hist()
		rows = append(rows,
			treeFg+"  │  Header    │ ChatViewport"+"\x1b[0m",
			toolRow(k%2 == 0),
			headStyle+heading+"\x1b[0m")
		scenes = append(scenes, fsScene(append(rows, band...), 2))
	}
	// Settle the tool row and let the border title change mid-row (the
	// "input top line half white" shape: the changed half must keep borderFg).
	rows := hist()
	rows = append(rows,
		treeFg+"  │  Overlays  │ Selector"+"\x1b[0m",
		toolRow(false),
		headStyle+"## What it is"+"\x1b[0m")
	settled := fsScene(append(rows, band...), 2)
	scenes = append(scenes, settled)
	retitled := fsScene(append(append([]string{}, rows...), band[0], band[1]), 2)
	retitled.Layers[0].Content[19] = borderFg + "╭─ claude " + strings.Repeat("─", 41) + "╮" + "\x1b[0m"
	scenes = append(scenes, retitled)

	fsRun(t, "colors", scenes)
}

// TestFilmstrip_TabSwitchVisualEquality swaps between two same-height canvases
// with wholesale content replacement (plus wide-grapheme cells), covering the
// full-repaint routes the diff path delegates to.
func TestFilmstrip_TabSwitchVisualEquality(t *testing.T) {
	mkTab := func(tag string) []string {
		rows := make([]string, 20)
		for i := range rows {
			suffix := ""
			if i%6 == 0 {
				suffix = " \U0001f1fa\U0001f1f8"
			}
			rows[i] = fmt.Sprintf("[%s] item %02d%s", tag, i, suffix)
		}
		return rows
	}
	a, b := mkTab("TAB-A"), mkTab("TAB-B")
	var scenes []*Scene
	for k := 0; k < 5; k++ {
		if k%2 == 0 {
			scenes = append(scenes, fsScene(a, 2))
		} else {
			scenes = append(scenes, fsScene(b, 2))
		}
	}
	fsRun(t, "tabswitch", scenes)
}

// TestFilmstrip_ChromeResizeVisualEquality grows and shrinks the pinned bottom
// band across frames while the transcript stays fixed — exercising the chrome
// mapping (prevChromeH bookkeeping) under partial emission.
func TestFilmstrip_ChromeResizeVisualEquality(t *testing.T) {
	transcript := make([]string, 15)
	for i := range transcript {
		transcript[i] = fmt.Sprintf("log-%02d stable transcript row", i)
	}
	editor := func(ch int) []string {
		rows := []string{"╭─editor──────────────────────────────╮"}
		for i := 0; i < ch-2; i++ {
			rows = append(rows, fmt.Sprintf("│ editor filler %02d                    │", i))
		}
		return append(rows, "╰─status──────────────────────────────╯")
	}
	var scenes []*Scene
	for _, ch := range []int{2, 4, 2, 3, 5} {
		rows := append(fsClone(transcript), editor(ch)...)
		scenes = append(scenes, fsScene(rows, ch))
	}
	fsRun(t, "chromeresize", scenes)
}
