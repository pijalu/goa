// SPDX-License-Identifier: GPL-3.0-or-later

package tui

import (
	"fmt"
	"testing"

	"github.com/pijalu/goa/internal/ansi"
)

// TestEditor_CursorAtEnd_DoubleSpaces is the end-to-end regression for the
// reported bug: typing a sentence containing double-spaces ("thecolor  should",
// "orange  and") while the text fits on one line placed the hardware cursor on
// the 'r' of "fire" — two columns before the real end — because the cursor was
// computed by collapsing spaces while the display preserved them. With the
// single-source-of-truth wrapChunks the cursor sits exactly at the end.
func TestEditor_CursorAtEnd_DoubleSpaces(t *testing.T) {
	text := "Create a html file that run a fire burning simulation - thecolor  should be blue, orange  and wjite to simulate a real burning fire"
	rs, engine, _, inp := newRenderScenario(t, 24, 200)
	inp.SetFocused(true)
	inp.SetText(text) // cursor at end
	engine.RenderNow()
	_, _, _, col := rs.snapshot()

	want := visibleWidth(text)
	if col != want {
		t.Errorf("hardware cursor col = %d, want %d (end of input). ", col, want)
		t.Errorf("the bug placed it at col %d (the 'r' of %q)", want-2, "fire")
	}
}

// chunkCursorInvariant asserts the central invariant of the single-source-of
// truth design: a cursor at the END of the text must land on the last visual
// line at the visible width of that line, where the visual lines ARE the wrap
// chunks. Display and cursor can never disagree because they share wrapChunks.
func chunkCursorInvariant(t *testing.T, text string, width int) {
	t.Helper()
	chunks := wrapChunks(text, width)
	pos := len([]rune(text))
	idx, off := cursorChunk(chunks, text, pos)

	wantIdx := len(chunks) - 1
	last := chunks[wantIdx]
	wantOff := last.End - last.Start
	bytePos := runeOffsetToByte(last.Text, wantOff)
	wantCol := ansi.Width(last.Text[:bytePos])
	gotCol := ansi.Width(last.Text[:runeOffsetToByte(last.Text, off)])

	if idx != wantIdx || off != wantOff || gotCol != wantCol {
		t.Errorf("text=%q width=%d\n  chunks=%q\n  cursor(END)=(idx=%d,off=%d,col=%d)\n  want=(idx=%d,off=%d,col=%d)",
			text, width, chunks, idx, off, gotCol, wantIdx, wantOff, wantCol)
	}
}

// TestWrapChunks_ObjectiveText is the regression for the reported bug: a long
// input line with double-spaces where the hardware cursor landed on the 'r' of
// "fire" instead of at the end. The old code collapsed spaces for the cursor
// but preserved them for display, so the two disagreed by one column per extra
// space. With wrapChunks as the single source of truth they always agree.
func TestWrapChunks_ObjectiveText(t *testing.T) {
	text := "Create a html file that run a fire burning simulation - thecolor  should be blue, orange  and wjite to simulate a real burning fire"
	for _, w := range []int{40, 60, 80, 100, 115, 120, 130, 150, 200} {
		t.Run(fmt.Sprintf("w%d", w), func(t *testing.T) {
			chunkCursorInvariant(t, text, w)
		})
	}
}

// TestWrapChunks_Semantics pins the wrapping rules against the cases the
// previous cursor tests encoded.
func TestWrapChunks_Semantics(t *testing.T) {
	cases := []struct {
		text  string
		width int
		want  []string // expected chunk texts
	}{
		{"hello", 80, []string{"hello"}},
		{"", 80, []string{""}},
		{"hello world", 5, []string{"hello", "world"}},     // boundary space consumed
		{"hello world  ", 5, []string{"hello", "world  "}}, // trailing spaces kept
		{"hello   ", 80, []string{"hello   "}},             // trailing spaces kept (no wrap)
		{"> this is ", 80, []string{"> this is "}},
		{"hello\nworld", 3, []string{"hel", "lo", "wor", "ld"}},
		{"a  b", 10, []string{"a  b"}}, // interior double space preserved (the bug)
		{"a  b", 3, []string{"a", "b"}},
	}
	for _, c := range cases {
		t.Run(fmt.Sprintf("%q_w%d", c.text, c.width), func(t *testing.T) {
			chunks := wrapChunks(c.text, c.width)
			got := make([]string, len(chunks))
			for i, ch := range chunks {
				got[i] = ch.Text
			}
			if fmt.Sprint(got) != fmt.Sprint(c.want) {
				t.Errorf("wrapChunks(%q,%d)=%q\n  want=%q", c.text, c.width, got, c.want)
			}
		})
	}
}
