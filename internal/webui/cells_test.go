// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/tui"
)

// The grid must turn the byte stream the engine actually emits into per-cell
// styling: SGR in, attributes out.
func TestCellGrid_SGRRoundTrip(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  tui.CellAttrs
	}{
		{"bold", "\x1b[1mB", tui.CellAttrs{Text: "B", Flags: tui.AttrBold}},
		{"italic underline", "\x1b[3;4mI", tui.CellAttrs{Text: "I", Flags: tui.AttrItalic | tui.AttrUnderline}},
		{"dim + strike", "\x1b[2;9mD", tui.CellAttrs{Text: "D", Flags: tui.AttrDim | tui.AttrStrike}},
		{"inverse", "\x1b[7mV", tui.CellAttrs{Text: "V", Flags: tui.AttrInverse}},
		{"truecolor fg", "\x1b[38;2;139;148;158mG", tui.CellAttrs{Text: "G", FG: "38;2;139;148;158"}},
		{"truecolor bg + bold", "\x1b[48;2;42;50;41;1mX", tui.CellAttrs{Text: "X", Flags: tui.AttrBold, BG: "48;2;42;50;41"}},
		{"256 palette fg", "\x1b[38;5;208mP", tui.CellAttrs{Text: "P", FG: "38;5;208"}},
		{"reset clears everything", "\x1b[1;38;2;1;2;3m\x1b[0mR", tui.CellAttrs{Text: "R"}},
		// The colour sub-parameters of an OSC-8 underline spec must never be
		// read as attributes (a channel of 2 would read as dim).
		{"underline colour must not read as dim", "\x1b[58;2;255;0;0mU", tui.CellAttrs{Text: "U"}},
		{"fg reset only", "\x1b[1m\x1b[39mF", tui.CellAttrs{Text: "F", Flags: tui.AttrBold}},
		{"bold off", "\x1b[1m\x1b[22mN", tui.CellAttrs{Text: "N"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			g := NewCellGrid(10, 2)
			g.Process(tc.input)
			if got := g.Cells(0)[0]; got != tc.want {
				t.Errorf("cell = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestCellGrid_SGRToHexRuns(t *testing.T) {
	g := NewCellGrid(10, 1)
	g.Process("\x1b[38;2;255;0;128mA\x1b[0mB\x1b[38;5;196mC")
	runs := trimTrailingBlanks(RowRuns(g.Cells(0)))
	if len(runs) != 3 {
		t.Fatalf("runs = %d, want 3: %+v", len(runs), runs)
	}
	if runs[0].FG != "#ff0080" {
		t.Errorf("truecolor fg = %q, want #ff0080", runs[0].FG)
	}
	if runs[1].FG != "" || runs[1].Flags != 0 {
		t.Errorf("default run should carry no styling: %+v", runs[1])
	}
	if runs[2].FG != "#ff0000" {
		t.Errorf("palette fg = %q, want #ff0000", runs[2].FG)
	}
}

// Only the rows that actually moved may appear in a patch — that is what keeps
// a streaming frame small.
func TestCellGrid_OnlyChangedRowsPatched(t *testing.T) {
	g := NewCellGrid(20, 4)
	g.Process("\x1b[3;1Hchanged")
	if p := g.Patches(); len(p) != 1 || p[0].Row != 2 {
		t.Fatalf("patches = %+v, want only row 2", p)
	}
	if p := g.Patches(); len(p) != 0 {
		t.Errorf("unchanged grid produced patches: %+v", p)
	}
}

// Scrollback must keep its styling: a row that scrolls off the top has to
// arrive with the colours it was written with.
func TestCellGrid_ScrollbackKeepsAttributes(t *testing.T) {
	g := NewCellGrid(10, 2)
	g.Process("\x1b[1;1H\x1b[38;2;1;2;3mred")
	g.Process("\x1b[2;1Htwo")
	g.Process("\x1b[3;1Hthree\n") // line feed at the bottom scrolls a row into scrollback

	rows := g.Scrollback()
	if len(rows) != 1 {
		t.Fatalf("scrollback rows = %d, want 1", len(rows))
	}
	if got := rows[0][0].FG; got != "38;2;1;2;3" {
		t.Errorf("scrollback fg = %q, want the colour the row was written with", got)
	}
	if txt := RunsText(RowRuns(rows[0])); !strings.HasPrefix(txt, "red") {
		t.Errorf("scrollback text = %q, want the row that scrolled off", txt)
	}
}

func TestCellGrid_ResizeReBaselinesAndFullPatches(t *testing.T) {
	g := NewCellGrid(10, 2)
	g.Process("\x1b[1;1Hhi")
	g.Resize(30, 5)
	if c, r := g.Size(); c != 30 || r != 5 {
		t.Fatalf("size = %dx%d, want 30x5", c, r)
	}
	if got := g.FullPatches(); len(got) != 5 {
		t.Errorf("full snapshot after resize = %d rows, want 5", len(got))
	}
}

func TestCellGrid_ClearBlanksScreen(t *testing.T) {
	g := NewCellGrid(6, 2)
	g.Process("\x1b[1;1Habc")
	g.Clear()
	if got := g.Text(); got != "\n" {
		t.Errorf("text after clear = %q", got)
	}
}

func TestCellGrid_ClampsGeometry(t *testing.T) {
	g := NewCellGrid(0, -5)
	if c, r := g.Size(); c != DefaultCols || r != DefaultRows {
		t.Errorf("default size = %dx%d", c, r)
	}
	g.Resize(100000, 100000)
	if c, r := g.Size(); c != maxCols || r != maxRows {
		t.Errorf("clamped size = %dx%d, want %dx%d", c, r, maxCols, maxRows)
	}
}

func TestPalette256(t *testing.T) {
	tests := []struct {
		n    int
		want string
	}{
		{0, "#000000"},
		{1, "#cd3131"},
		{15, "#ffffff"},
		{16, "#000000"},
		{196, "#ff0000"},
		{231, "#ffffff"},
		{232, "#080808"},
		{255, "#eeeeee"},
		{-1, ""},
		{256, ""},
	}
	for _, tc := range tests {
		if got := Palette256(tc.n); got != tc.want {
			t.Errorf("Palette256(%d) = %q, want %q", tc.n, got, tc.want)
		}
	}
}

func TestSGRToCSS(t *testing.T) {
	tests := []struct {
		in, want string
	}{
		{"", ""},
		{"38;2;255;255;255", "#ffffff"},
		{"48;5;21", Palette256(21)},
		{"38;2;300;0;0", "#ff0000"}, // clamped
		{"38;2;abc;0;0", ""},        // unparseable → theme default
		{"38;5", ""},                // truncated
		{"39", ""},                  // not a colour spec
		{"junk", ""},
	}
	for _, tc := range tests {
		if got := sgrToCSS(tc.in); got != tc.want {
			t.Errorf("sgrToCSS(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// trimTrailingBlanks drops the trailing unstyled space run every padded row
// ends with, so assertions can count the styled runs.
func trimTrailingBlanks(runs []Run) []Run {
	for len(runs) > 0 {
		last := runs[len(runs)-1]
		if last.FG == "" && last.BG == "" && last.Flags == 0 && strings.TrimSpace(last.Text) == "" {
			runs = runs[:len(runs)-1]
			continue
		}
		break
	}
	return runs
}
