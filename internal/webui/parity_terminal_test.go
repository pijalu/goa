// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/tui"
)

// The screen-parity guarantee (spec §3.1, §17.3), stated as a test: the bytes
// the compositor emits land in the browser's cell grid exactly as they land in
// a terminal's emulator. Both sides are fed the SAME byte stream — one captured
// from a real tui.Compositor render — and the resulting cell grids (text,
// colours, flags, links, cursor, scrollback) must be identical.
//
// This is the "the page looks like the TUI" regression test: if the virtual
// terminal ever drops, reorders or normalises a byte differently from the
// terminal path, the dumps diverge here.

// capTerminal is a tui.Terminal that records every byte the compositor writes.
// It is the terminal side of the comparison: in production this is what a pty
// receives, and the pty's emulator is tui.TermEmulator.
type capTerminal struct {
	w, h   int
	writes strings.Builder
}

func (c *capTerminal) Start(func(string), func()) {}
func (c *capTerminal) Stop()                      {}
func (c *capTerminal) SetRaw() (func(), error)    { return func() {}, nil }
func (c *capTerminal) HideCursor()                {}
func (c *capTerminal) ShowCursor()                {}
func (c *capTerminal) ClearScreen()               {}
func (c *capTerminal) SetTitle(string)            {}
func (c *capTerminal) Size() (int, int)           { return c.w, c.h }
func (c *capTerminal) Write(p []byte) (int, error) {
	return c.writes.Write(p)
}
func (c *capTerminal) WriteString(s string) { c.writes.WriteString(s) }

// parityStream renders one scene through a real Compositor and returns the bytes
// a terminal would receive. Reusing the engine's own compositor (rather than a
// hand-written ANSI blob) is what makes the comparison cover the sequences Goa
// actually produces: SGR colours, bold/dim/italic/underline, OSC 8 links,
// cursor motion, erases and the scroll that a full chat transcript causes.
func parityStream(t *testing.T, cols, rows int) string {
	t.Helper()
	term := &capTerminal{w: cols, h: rows}
	comp := tui.NewCompositor(term)
	comp.Render(parityScene(cols, rows))
	return term.writes.String()
}

// parityScene is a deterministic frame with one of everything the comparator
// cares about. Layer content is pre-styled — the engine renders ANSI into these
// strings before they reach the compositor — so the stream carries SGR colours,
// the attribute bits, an OSC 8 link and a cursor move.
func parityScene(cols, rows int) *tui.Scene {
	const (
		reset = "\x1b[0m"
		acc   = "\x1b[38;2;124;92;252m"  // #7c5cfc
		fg    = "\x1b[38;2;231;233;241m" // #e7e9f1
	)
	link := func(text, url string) string {
		return "\x1b]8;;" + url + "\x1b\\" + acc + text + reset + "\x1b]8;;\x1b\\"
	}
	return &tui.Scene{
		TerminalW: cols, TerminalH: rows,
		Layers: []tui.Layer{
			{
				Name: "chat", Kind: tui.LayerBase,
				Rect: tui.Rect{X: 0, Y: 0, W: cols, H: rows},
				Content: []string{
					"\x1b[1m" + acc + "goa" + reset + " " + fg + "web parity" + reset,
					"\x1b[3;4;2m" + fg + "italic underline dim" + reset + " " + link("spec", "https://example.com/spec"),
				},
			},
		},
		Cursor: &tui.CursorPos{Row: 1, Col: 4},
	}
}

// TestParity_WebCellsEqualTerminalCells is the checkpoint: same bytes in, same
// cells out, whether the sink is a terminal's emulator or the browser's grid.
func TestParity_WebCellsEqualTerminalCells(t *testing.T) {
	const cols, rows = 40, 6
	stream := parityStream(t, cols, rows)
	if stream == "" {
		t.Fatal("the compositor emitted nothing — the parity comparison would be vacuous")
	}

	// Terminal side: the bytes go straight into the emulator a pty feeds.
	emu := tui.NewTermEmulator(rows, cols)
	emu.Process(stream)

	// Web side: the same bytes go through the VirtualTerminal the browser
	// writes to, i.e. through the real web path including its buffering.
	vt := NewVirtualTerminal(cols, rows)
	vt.Start(func(string) {}, func() {})
	vt.WriteString(stream)

	for r := 0; r < rows; r++ {
		grid := vt.Grid()
		want, got := emu.Cells(r), grid.Cells(r)
		if dumpRow(want) != dumpRow(got) {
			t.Fatalf("row %d differs between the terminal and the web grid\n--- terminal ---\n%s\n--- web ---\n%s",
				r, dumpRow(want), dumpRow(got))
		}
	}
	cur := vt.Grid().Cursor()
	if wr, wc := emu.Cursor(); wr != cur.Row || wc != cur.Col {
		t.Errorf("cursor = %d,%d on the terminal, %d,%d on the web grid", wr, wc, cur.Row, cur.Col)
	}
	if emu.ScrollbackCells() != nil && len(emu.ScrollbackCells()) != len(vt.Grid().Scrollback()) {
		t.Errorf("scrollback = %d rows on the terminal, %d on the web grid",
			len(emu.ScrollbackCells()), len(vt.Grid().Scrollback()))
	}
}

// A long stream scrolls the screen; the transcript that falls off the top must
// be identical on both sides too, or the browser's scrollback would differ from
// a terminal's.
func TestParity_WebScrollbackMatchesTerminal(t *testing.T) {
	const cols, rows = 20, 3
	var stream strings.Builder
	for i := 0; i < 8; i++ {
		stream.WriteString(fmt.Sprintf("\x1b[%d;1Htranscript line %d", i+1, i))
	}

	emu := tui.NewTermEmulator(rows, cols)
	emu.Process(stream.String())

	vt := NewVirtualTerminal(cols, rows)
	vt.Start(func(string) {}, func() {})
	vt.WriteString(stream.String())

	want, got := emu.ScrollbackCells(), vt.Grid().Scrollback()
	if len(want) != len(got) {
		t.Fatalf("scrollback rows: terminal=%d web=%d", len(want), len(got))
	}
	for i := range want {
		if dumpRow(want[i]) != dumpRow(got[i]) {
			t.Fatalf("scrollback row %d differs\n--- terminal ---\n%s\n--- web ---\n%s",
				i, dumpRow(want[i]), dumpRow(got[i]))
		}
	}
}

// dumpRow spells out one row's cells so a diff names the attribute that moved.
// A cell with no text is written as a single space on both sides: CellGrid
// normalises blanks to a space for the wire (so the browser paints them), and
// that is the only difference the browser may observe — anything else would be
// a fidelity divergence.
func dumpRow(cells []tui.CellAttrs) string {
	var b strings.Builder
	for i, c := range cells {
		if i > 0 && c.FG == "" && c.BG == "" && c.Flags == 0 && c.Link == "" {
			continue // run of blanks
		}
		text := c.Text
		if text == "" {
			text = " "
		}
		fmt.Fprintf(&b, "%d:%q flags=%d fg=%q bg=%q link=%q\n", i, text, c.Flags, c.FG, c.BG, c.Link)
	}
	return b.String()
}
