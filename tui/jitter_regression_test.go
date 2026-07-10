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
				Rect: Rect{X: 0, Y: 0, W: w, H: h},
				Content: []string{spinTop, sep, "hello", sep, spinBottom},
			}},
		}
	}

	term := &fakeTerminal{w: w, h: h}
	comp := NewCompositor(term)

	comp.Render(frame("spin-A-top", "spin-A-bottom"))
	term.writes = nil // drop the first-frame full render; capture only the diff

	// Second frame: ONLY y=0 and y=4 changed (a spinner tick), no scrolling.
	comp.Render(frame("spin-B-top", "spin-B-bottom"))

	diff := strings.Join(term.writes, "")

	// Separators live at canvas rows 1 and 3. With canvas height == terminal
	// height there is no scrolling, so viewportTop stays 0 and their screen
	// rows are 2 and 4 (1-indexed CUP). Erasing either proves an unchanged
	// separator was needlessly rewritten.
	if rowErased(diff, 2) {
		t.Errorf("UNCHANGED top separator (screen row 2) was erased+rewritten by the diff:\n%s", diff)
	}
	if rowErased(diff, 4) {
		t.Errorf("UNCHANGED bottom separator (screen row 4) was erased+rewritten by the diff:\n%s", diff)
	}
	if rowErased(diff, 3) {
		t.Errorf("UNCHANGED editor content (screen row 3) was erased+rewritten by the diff:\n%s", diff)
	}
	// Sanity: the two lines that genuinely changed MUST be rewritten.
	if !rowErased(diff, 1) {
		t.Errorf("changed status line (screen row 1) was NOT rewritten:\n%s", diff)
	}
	if !rowErased(diff, 5) {
		t.Errorf("changed footer line (screen row 5) was NOT rewritten:\n%s", diff)
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
		open := strings.Index(w, "\x1b[?2026h")
		close := strings.Index(w, "\x1b[?2026l")
		if open < 0 || close < open {
			continue
		}
		body := w[open:close]
		// Find a CUP that is not immediately a line-erase (content clears are
		// \x1b[r;1H\x1b[2K; the cursor CUP has a non-1 column or no trailing 2K).
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
	}
	return false
}
