// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package attach

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/webui"
	"github.com/pijalu/goa/tui"
)

// bufWriter is the renderer's test sink: it captures the byte stream a real
// terminal would receive.
type bufWriter struct{ b strings.Builder }

func (w *bufWriter) WriteString(s string) { w.b.WriteString(s) }

// The pen's SGR encoder: attributes in the classic order, colours as
// truecolor parameters, defaults emitted as 39/49 — and no sequence at all
// for the default pen.
func TestPenSGR(t *testing.T) {
	cases := []struct {
		name string
		pen  pen
		want string
	}{
		{"default", pen{}, ""},
		{"bold", pen{flags: tui.AttrBold}, "\x1b[1;39;49m"},
		{"dim+underline", pen{flags: tui.AttrDim | tui.AttrUnderline}, "\x1b[2;4;39;49m"},
		{"inverse+strike", pen{flags: tui.AttrInverse | tui.AttrStrike}, "\x1b[7;9;39;49m"},
		{"italic", pen{flags: tui.AttrItalic}, "\x1b[3;39;49m"},
		{"truecolor", pen{fg: "#102030", bg: "#fedcba"}, "\x1b[38;2;16;32;48;48;2;254;220;186m"},
		{"fg only", pen{fg: "#ff0000"}, "\x1b[38;2;255;0;0;49m"},
	}
	for _, c := range cases {
		if got := c.pen.sgr(); got != c.want {
			t.Errorf("%s: sgr = %q, want %q", c.name, got, c.want)
		}
	}
}

func TestRGB(t *testing.T) {
	if got := rgb("#0a0b0c"); got != "10;11;12" {
		t.Errorf("rgb = %q", got)
	}
	if got := rgb("zzz"); got != "0;0;0" {
		t.Errorf("malformed rgb = %q, want black", got)
	}
}

// A painted row: absolute positioning, the runs' text with style changes,
// and a reset + erase that blanks the tail and leaves a clean pen.
func TestScreenPaintsRow(t *testing.T) {
	var w bufWriter
	s := NewScreen(&w, 20, 3)
	s.Start()
	w.b.Reset()

	f := &webui.Frame{
		Seq: 1, Cols: 20, Rows: 3,
		Cursor: webui.Cursor{Row: 0, Col: 3, Visible: true},
		Patches: []webui.RowPatch{
			{Row: 0, Runs: []webui.Run{
				{Text: "goa", Flags: tui.AttrBold, FG: "#00ff00"},
				{Text: " rest"},
			}},
		},
	}
	s.Frame(f, nil)

	out := w.b.String()
	for _, want := range []string{
		"\x1b[1;1H",               // row 0
		"\x1b[1;38;2;0;255;0;49m", // bold + green fg, one absolute SGR
		"goa",
		"\x1b[0m rest",  // default pen is an explicit reset
		"\x1b[0m\x1b[K", // tail reset+erase
		"\x1b[1;4H",     // cursor to col 3
		"\x1b[?25h",     // caret shown
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q in %q", want, out)
		}
	}
}

// The transcript stamp: each row is written at the top line and scrolled off,
// and a replacement batch wipes the native scrollback first.
func TestScreenWriteTranscript(t *testing.T) {
	var w bufWriter
	s := NewScreen(&w, 10, 4)
	s.Start()
	w.b.Reset()

	batch := webui.TranscriptBatch{Rows: []webui.RowPatch{
		{Row: 0, Runs: []webui.Run{{Text: "one"}}},
		{Row: 1, Runs: []webui.Run{{Text: "two"}}},
	}}
	s.writeTranscript(batch)
	out := w.b.String()
	// Two stamps: CUP(1,1) … CUP(4,1) + LF each; no 3J (not a replacement).
	if got := strings.Count(out, "\x1b[1;1H"); got != 2 {
		t.Errorf("stamps at top = %d, want 2", got)
	}
	if got := strings.Count(out, "\x1b[4;1H\n"); got != 2 {
		t.Errorf("scroll pushes = %d, want 2", got)
	}
	if strings.Contains(out, eraseScrol) {
		t.Errorf("append batch wiped scrollback: %q", out)
	}

	w.b.Reset()
	s.writeTranscript(webui.TranscriptBatch{Replace: true, Rows: batch.Rows})
	if !strings.Contains(w.b.String(), eraseScrol) {
		t.Errorf("replacement batch must erase native scrollback: %q", w.b.String())
	}
}

// Live scroll: k line feeds at the bottom of the screen push the top k lines
// into native scrollback without touching the picture.
func TestScreenScrollUp(t *testing.T) {
	var w bufWriter
	s := NewScreen(&w, 10, 4)
	s.Start()
	w.b.Reset()
	s.scrollUp(3)
	if got := strings.Count(w.b.String(), "\n"); got != 3 {
		t.Errorf("line feeds = %d, want 3", got)
	}
	if !strings.Contains(w.b.String(), "\x1b[4;1H") {
		t.Errorf("scroll must feed from the bottom row: %q", w.b.String())
	}
}

// THE contract of the attach renderer: the frames the server produces, run
// through the Screen, must reconstruct the server's grid byte-for-byte in a
// real terminal emulator — same cells, same colours, same caret.
func TestScreenRoundTripsServerFrames(t *testing.T) {
	const cols, rows = 40, 8
	grid := webui.NewCellGrid(cols, rows)

	// A script exercising text, SGR styles, absolute positioning, scrolling
	// and clearing — the vocabulary the compositor actually emits. No OSC:
	// the title and links are asserted byte-exact elsewhere (the emulator is
	// the server's own parser, and it does not consume OSC).
	script := []string{
		"\x1b[1;1H\x1b[1mgoa attach\x1b[0m — round trip",
		"\x1b[2;1H\x1b[32mgreen\x1b[0m \x1b[41;97mwhite on red\x1b[0m \x1b[4munder\x1b[0m",
		"\x1b[3;5Hindented",
		"\x1b[4;1H\x1b[2mdim line\x1b[0m",
		"\x1b[5;1H\x1b[3mitalic\x1b[0m",
		"\x1b[6;1H\x1b[7minverse\x1b[0m \x1b[9mstrike\x1b[0m",
		"\x1b[8;1Hscroll me\r\n", // pushes row 1 into scrollback
	}
	var out bufWriter
	scr := NewScreen(&out, cols, rows)
	scr.Attach(attachFrame(grid, script[0]), attachBatch(grid))
	driveScript(t, grid, scr, script[1:])

	// Replay the byte stream into a fresh emulator and compare screens.
	emu := tui.NewTermEmulator(rows, cols)
	emu.Process(out.b.String())
	assertEmulatorMatchesGrid(t, grid, emu, rows, out.b.String())
	if sb := emu.ScrollbackCells(); len(sb) == 0 {
		t.Error("terminal scrollback is empty; scrolled-off rows were not pushed")
	}
}

// attachFrame ships the grid's full state as the snapshot a new client gets.
func attachFrame(grid *webui.CellGrid, first string) *webui.Frame {
	grid.Process(first)
	return webui.NewFrame(1, grid, grid.FullPatches(), grid.Title(), true)
}

// attachBatch drains the transcript batch the attach snapshot carries.
func attachBatch(grid *webui.CellGrid) *webui.TranscriptBatch {
	if b := grid.TakeScrollback(); len(b.Rows) > 0 || b.Replace {
		return &b
	}
	return nil
}

// driveScript feeds the remaining script lines as live deltas, riding each
// frame's transcript batch when one is due.
func driveScript(t *testing.T, grid *webui.CellGrid, scr *Screen, script []string) {
	t.Helper()
	for i, line := range script {
		grid.Process(line)
		f := webui.NewFrame(uint64(i+2), grid, grid.Patches(), grid.Title(), false)
		scr.Frame(f, attachBatch(grid))
	}
}

// assertEmulatorMatchesGrid compares every cell and the caret.
func assertEmulatorMatchesGrid(t *testing.T, grid *webui.CellGrid, emu *tui.TermEmulator, rows int, dump string) {
	t.Helper()
	for r := 0; r < rows; r++ {
		want, got := grid.Cells(r), normalizeCellsForTest(emu.Cells(r))
		if len(want) != len(got) {
			t.Fatalf("row %d length: server=%d terminal=%d", r, len(want), len(got))
		}
		for c := range want {
			if want[c] != got[c] {
				t.Fatalf("row %d col %d: server %+v, terminal %+v\nscreen:\n%s", r, c, want[c], got[c], dump)
			}
		}
	}
	cur := grid.Cursor()
	gotRow, gotCol := emu.Cursor()
	if cur.Row != gotRow || cur.Col != gotCol {
		t.Errorf("cursor: server (%d,%d), terminal (%d,%d)", cur.Row, cur.Col, gotRow, gotCol)
	}
	if cur.Visible != emu.CursorVisible() {
		t.Errorf("cursor visibility: server %v, terminal %v", cur.Visible, emu.CursorVisible())
	}
}

// normalizeCellsForTest mirrors the grid's blank-cell normalisation so a raw
// emulator row is comparable with the grid's Cells output.
func normalizeCellsForTest(cells []tui.CellAttrs) []tui.CellAttrs {
	out := make([]tui.CellAttrs, len(cells))
	copy(out, cells)
	for i := range out {
		if out[i].Text == "" {
			out[i].Text = " "
		}
	}
	return out
}

// A geometry change repaints the whole screen in the new size.
func TestScreenGeometryChangeRepaints(t *testing.T) {
	var w bufWriter
	s := NewScreen(&w, 20, 4)
	s.Start()
	s.Frame(&webui.Frame{
		Seq: 1, Cols: 20, Rows: 4,
		Cursor:  webui.Cursor{Row: 0, Col: 0, Visible: true},
		Patches: []webui.RowPatch{{Row: 0, Runs: []webui.Run{{Text: "hello"}}}},
	}, nil)
	w.b.Reset()
	s.Frame(&webui.Frame{
		Seq: 2, Cols: 30, Rows: 6, Full: true,
		Cursor: webui.Cursor{Row: 0, Col: 5, Visible: true},
		Patches: []webui.RowPatch{
			{Row: 0, Runs: []webui.Run{{Text: "hello resize"}}},
			{Row: 5, Runs: []webui.Run{{Text: "new bottom"}}},
		},
	}, nil)
	out := w.b.String()
	if !strings.Contains(out, clearScreen) {
		t.Errorf("geometry change must clear before repainting: %q", out)
	}
	if !strings.Contains(out, "\x1b[6;1H") || !strings.Contains(out, "new bottom") {
		t.Errorf("geometry change must paint rows of the new size: %q", out)
	}
}

// A row painted edge to edge must NOT be followed by an erase: the terminal
// sits in pending-wrap after the last column, and an EL issued there erases
// the last written cell — the row arrived one column short (caught by the
// filmstrip wire-parity test on the footer separator rows).
func TestScreenFullWidthRowKeepsLastCell(t *testing.T) {
	var w bufWriter
	s := NewScreen(&w, 10, 2)
	s.Start()
	s.Frame(&webui.Frame{
		Seq: 1, Cols: 10, Rows: 2, Full: true,
		Cursor: webui.Cursor{Row: 0, Col: 0, Visible: true},
		Patches: []webui.RowPatch{
			{Row: 0, Runs: []webui.Run{{Text: "──────────"}}},
			{Row: 1, Runs: []webui.Run{{Text: "short"}}},
		},
	}, nil)
	out := w.b.String()
	first := strings.Index(out, "──────────")
	rest := out[first+len("──────────"):]
	if strings.HasPrefix(rest, eraseBelow) || strings.HasPrefix(rest, "\x1b[0m\x1b[K") {
		t.Errorf("full-width row was followed by an erase: %q", rest[:minStr(12, len(rest))])
	}
	// The underfilled row still gets its tail blanked.
	if !strings.Contains(out, "\x1b[0m\x1b[K") {
		t.Errorf("underfilled row lost its tail erase: %q", out)
	}
}

func minStr(a, b int) int {
	if a < b {
		return a
	}
	return b
}
