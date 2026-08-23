// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strconv"
	"strings"
	"testing"
)

// TestCompositor_DiffSkipsUnchangedLinesBetweenChangingRegions reproduces and
// guards the "input-line / separator jitter".
//
// During streaming the status spinner (above the editor) and the footer
// busy-frame (below it) change every tick, while the editor's full-width
// separator lines never change. A differential renderer must leave those
// byte-identical lines untouched. The implementation previously computed only
// the changed *range* [first, last] and then unconditionally erased (\x1b[2K)
// and rewrote every line in that range — including the unchanged separators
// sandwiched between the two changing regions — which flickered every tick
// (worst case for full-width lines because they arm the terminal's deferred
// auto-wrap). This test asserts the unchanged separator/content rows are NOT
// erased on a no-scroll frame, while the genuinely-changed rows ARE.
func TestCompositor_DiffSkipsUnchangedLinesBetweenChangingRegions(t *testing.T) {
	const w, h = 20, 5
	sep := strings.Repeat("─", w) // editor border line: full-width, unchanged

	// Layout mirrors the real bottom-of-screen stack during a spinner tick:
	//   y=0  status spinner       -> CHANGES every frame
	//   y=1  editor top border    -> UNCHANGED (separator)
	//   y=2  editor content       -> UNCHANGED
	//   y=3  editor bottom border -> UNCHANGED (separator)
	//   y=4  footer busy frame    -> CHANGES every frame
	frame := func(spinTop, spinBottom string) *Scene {
		return &Scene{
			TerminalW: w, TerminalH: h,
			Layers: []Layer{{
				Name: "stack", Kind: LayerBase,
				Rect:    Rect{X: 0, Y: 0, W: w, H: h},
				Content: []string{spinTop, sep, "hello", sep, spinBottom},
			}},
		}
	}
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)

	comp.Render(frame("spin-A-top", "spin-A-bottom"))
	firstFrame := term.Writes()
	term.writes = nil // drop the first-frame full render; capture only the diff

	// Second frame: ONLY y=0 and y=4 changed (a spinner tick), no scrolling.
	comp.Render(frame("spin-B-top", "spin-B-bottom"))

	diff := strings.Join(term.writes, "")

	// Separators live at canvas rows 1 and 3. With canvas height == terminal
	// height there is no scrolling, so viewportTop stays 0 and their screen
	// rows are 2 and 4 (1-indexed CUP). Erasing either proves an unchanged
	// separator was needlessly rewritten.
	for _, row := range []struct {
		screenRow int
		what      string
	}{
		{2, "top separator"}, {4, "bottom separator"}, {3, "editor content"},
	} {
		if rowErased(diff, row.screenRow) {
			t.Errorf("UNCHANGED %s (screen row %d) was erased+rewritten by the diff:\n%s", row.what, row.screenRow, diff)
		}
	}
	assertDiffFrameScreenIntegrity(t, emuForClampedScene(w, h, firstFrame), term.Writes())
}

// emuForClampedScene replays first-frame writes into a TermEmulator sized to
// the EFFECTIVE terminal the compositor drives: Render clamps undersized
// scenes (height<10 → 24 rows, width<20 → 80 cols).
func emuForClampedScene(w, h int, firstFrame []string) *TermEmulator {
	effW, effH := w, h
	if effW < 20 {
		effW = 80
	}
	if effH < 10 {
		effH = 24
	}
	emu := NewTermEmulator(effH, effW)
	for _, wr := range firstFrame {
		emu.Process(wr)
	}
	return emu
}

// assertDiffFrameScreenIntegrity verifies a captured diff frame through the
// emulator: the genuinely changed rows show their NEW content and every
// other row stays CELL-identical to the pre-diff frame — a strictly stronger
// check than an erase-byte proxy, because column-range emission may repaint
// via partial CUP+segment instead of a full-row erase.
func assertDiffFrameScreenIntegrity(t *testing.T, emu *TermEmulator, diffWrites []string) {
	t.Helper()
	prevRows := make([]string, emu.h)
	for r := 0; r < emu.h; r++ {
		prevRows[r] = emu.Visible(r)
	}
	for _, wr := range diffWrites {
		emu.Process(wr)
	}
	if got, want := emu.Visible(0), "spin-B-top"; !strings.Contains(got, want) {
		t.Errorf("changed status line (screen row 1) does not show %q:\n%s", want, got)
	}
	if got, want := emu.Visible(4), "spin-B-bottom"; !strings.Contains(got, want) {
		t.Errorf("changed footer line (screen row 5) does not show %q:\n%s", want, got)
	}
	for r := 1; r <= 3; r++ {
		if got := emu.Visible(r); got != prevRows[r] {
			t.Errorf("UNCHANGED screen row %d mutated by the diff: %q -> %q", r+1, prevRows[r], got)
		}
	}
}

// TestCompositor_CursorInsideSync asserts the hardware-cursor repositioning is
// emitted INSIDE a CSI 2026 synchronized-output region, so the cursor is
// restored atomically with the content rather than in a separate,
// unsynchronized write that flashes between the content flush and the cursor
// move.
func TestCompositor_CursorInsideSync(t *testing.T) {
	const w, h = 10, 4
	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)

	content := []string{"a", "b", "c", "d"}
	scene := func(cursor *CursorPos) *Scene {
		return &Scene{
			TerminalW: w, TerminalH: h,
			Layers: []Layer{{
				Name: "stack", Kind: LayerBase,
				Rect: Rect{X: 0, Y: 0, W: w, H: h}, Content: content,
			}},
			Cursor: cursor,
		}
	}

	comp.Render(scene(&CursorPos{Row: 3, Col: 1})) // establish baseline + shown cursor
	term.writes = nil
	comp.Render(scene(&CursorPos{Row: 3, Col: 2})) // cursor moves, content unchanged

	// The cursor move is a CUP of the form ESC[r;cH. A content-line clear is
	// always ESC[r;1H followed immediately by ESC[2K, so a CUP inside the sync
	// whose column is not 1 (or not followed by ESC[2K) is the cursor seq. It
	// must appear between a ?2026h and its matching ?2026l in the SAME write.
	if !cursorSeqIsSynced(term.writes) {
		t.Errorf("cursor move was not emitted inside a CSI 2026 sync:\n%q", strings.Join(term.writes, "\n---\n"))
	}
}

// rowErased reports whether the diff erased (ESC[r;1H ESC[2K) the given
// 1-indexed screen row — i.e. whether that row was part of the rewrite span.
func rowErased(diff string, row int) bool {
	return strings.Contains(diff, "\x1b["+strconv.Itoa(row)+";1H\x1b[2K")
}

// cursorSeqIsSynced reports whether some write contains a cursor-positioning
// CUP (a row;col move that is NOT a content-line clear) bracketed by ?2026h /
// ?2026l in that same write — i.e. the cursor is restored atomically with the
// frame content under synchronized output.
func cursorSeqIsSynced(writes []string) bool {
	for _, w := range writes {
		if writeHasCursorInSync(w) {
			return true
		}
	}
	return false
}

func writeHasCursorInSync(w string) bool {
	open := strings.Index(w, "\x1b[?2026h")
	close := strings.Index(w, "\x1b[?2026l")
	if open < 0 || close < open {
		return false
	}
	body := w[open:close]
	return bodyHasCursorCUP(body)
}

func bodyHasCursorCUP(body string) bool {
	for i := 0; i < len(body); i++ {
		if !strings.HasPrefix(body[i:], "\x1b[") {
			continue
		}
		end := strings.IndexByte(body[i:], 'H')
		if end < 0 {
			break
		}
		cup := body[i : i+end+1]
		i += end + 1
		if !strings.Contains(cup, ";") {
			continue
		}
		if !strings.HasSuffix(cup, ";1H") || !strings.HasPrefix(body[i:], "\x1b[2K") {
			return true // a CUP that is not a content-line clear = cursor seq
		}
	}
	return false
}
