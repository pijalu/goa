// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"strings"
	"testing"
)

func TestTermEmulator_CellsExposeTextAndAttributes(t *testing.T) {
	e := NewTermEmulator(3, 10)
	e.Process("\x1b[1;1H\x1b[1;38;2;1;2;3mgoa\x1b[0m plain")

	row := e.Cells(0)
	if len(row) != 10 {
		t.Fatalf("cells = %d, want 10", len(row))
	}
	if row[0].Text != "g" || row[0].Flags != AttrBold || row[0].FG != "38;2;1;2;3" {
		t.Errorf("cell 0 = %+v", row[0])
	}
	if row[3].Flags != 0 || row[3].FG != "" {
		t.Errorf("cell 3 should be default-styled: %+v", row[3])
	}
	// Untouched cells stay empty in the raw API; the web layer normalises them.
	if row[9].Text != "" {
		t.Errorf("untouched cell = %q, want empty", row[9].Text)
	}
	if e.CellsText(0)[:3] != "goa" {
		t.Errorf("CellsText = %q", e.CellsText(0))
	}
}

func TestTermEmulator_CellOutOfRangeIsZero(t *testing.T) {
	e := NewTermEmulator(2, 4)
	if got := e.Cell(-1, 0); got != (CellAttrs{}) {
		t.Errorf("Cell(-1,0) = %+v", got)
	}
	if got := e.Cell(0, 9); got != (CellAttrs{}) {
		t.Errorf("Cell(0,9) = %+v", got)
	}
	if e.Cells(5) != nil {
		t.Error("Cells(5) should be nil")
	}
}

func TestTermEmulator_TrackSGRFlags(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want AttrFlags
	}{
		{"bold", "\x1b[1m", AttrBold},
		{"dim", "\x1b[2m", AttrDim},
		{"italic", "\x1b[3m", AttrItalic},
		{"underline", "\x1b[4m", AttrUnderline},
		{"inverse", "\x1b[7m", AttrInverse},
		{"strike", "\x1b[9m", AttrStrike},
		{"combined", "\x1b[1;3;4m", AttrBold | AttrItalic | AttrUnderline},
		{"reset", "\x1b[1m\x1b[0m", 0},
		{"bold off via 22", "\x1b[1m\x1b[22m", 0},
		{"underline off via 24", "\x1b[4m\x1b[24m", 0},
		// A colour channel must never be read as an attribute code.
		{"truecolour channel 2 is not dim", "\x1b[38;2;1;2;3m", 0},
		{"underline colour channel 1 is not bold", "\x1b[58;2;1;2;3m", 0},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e := NewTermEmulator(1, 4)
			e.Process(tc.in + "X")
			if got := e.Cell(0, 0).Flags; got != tc.want {
				t.Errorf("flags = %s, want %s", got, tc.want)
			}
		})
	}
}

func TestTermEmulator_AttrFlagsString(t *testing.T) {
	if got := AttrFlags(0).String(); got != "default" {
		t.Errorf("zero flags = %q", got)
	}
	if got := (AttrBold | AttrItalic).String(); got != "bold+italic" {
		t.Errorf("bold+italic = %q", got)
	}
}

// A row that scrolls off must keep its colours — the web client ships
// scrollback rows with styling intact.
func TestTermEmulator_ScrollbackKeepsForeground(t *testing.T) {
	e := NewTermEmulator(2, 8)
	e.Process("\x1b[1;1H\x1b[38;2;9;9;9mred")
	e.Process("\x1b[2;1Htwo")
	e.Process("\x1b[3;1Hthree\n")

	rows := e.ScrollbackCells()
	if len(rows) != 1 {
		t.Fatalf("scrollback = %d rows, want 1", len(rows))
	}
	if rows[0][0].FG != "38;2;9;9;9" {
		t.Errorf("scrollback fg = %q", rows[0][0].FG)
	}
	// The cursor was already on the last row when the scroll happened, so the
	// newly written row moves up and the bottom opens blank.
	if got := e.Visible(0); got[:5] != "three" {
		t.Errorf("row 0 after scroll = %q, want the shifted-up content", got)
	}
	if got := strings.TrimSpace(e.Visible(1)); got != "" {
		t.Errorf("fresh bottom row = %q, want blank", got)
	}
}

func TestTermEmulator_DirtyRowTracking(t *testing.T) {
	e := NewTermEmulator(3, 5)
	// Tracking off: no bookkeeping, nil drain.
	e.Process("\x1b[1;1Hx")
	if got := e.DrainDirty(); got != nil {
		t.Errorf("drain with tracking off = %v, want nil", got)
	}

	e.TrackDirty(true)
	e.Process("\x1b[2;1Hy")
	got := e.DrainDirty()
	if len(got) != 1 || got[0] != 1 {
		t.Fatalf("drain = %v, want [1]", got)
	}
	if again := e.DrainDirty(); len(again) != 0 {
		t.Errorf("second drain = %v, want empty", again)
	}

	e.MarkDirty()
	if all := e.DrainDirty(); len(all) != 3 {
		t.Errorf("MarkDirty drain = %v, want all rows", all)
	}

	e.TrackDirty(false)
	e.Process("\x1b[3;1Hz")
	if got := e.DrainDirty(); got != nil {
		t.Errorf("drain after disable = %v, want nil", got)
	}
}

func TestTermEmulator_ResizePreservesContent(t *testing.T) {
	e := NewTermEmulator(3, 10)
	e.Process("\x1b[1;1Habc")

	e.Resize(5, 2) // narrower and shorter
	if w, h := e.Size(); w != 5 || h != 2 {
		t.Fatalf("size = %dx%d", w, h)
	}
	if got := e.Visible(0); got[:3] != "abc" {
		t.Errorf("row 0 after resize = %q", got)
	}
	if len(e.Cells(1)) != 5 {
		t.Errorf("row width = %d, want 5", len(e.Cells(1)))
	}

	e.Resize(20, 4) // grow again
	if w, h := e.Size(); w != 20 || h != 4 {
		t.Fatalf("grown size = %dx%d", w, h)
	}
	if len(e.Cells(3)) != 20 {
		t.Errorf("grown row width = %d, want 20", len(e.Cells(3)))
	}
	if got := e.Visible(0); got[:3] != "abc" {
		t.Errorf("row 0 after grow = %q", got)
	}
}

func TestTermEmulator_ResizeClampsDegenerateSizes(t *testing.T) {
	e := NewTermEmulator(3, 10)
	e.Resize(0, -5)
	if w, h := e.Size(); w != 1 || h != 1 {
		t.Errorf("clamped size = %dx%d, want 1x1", w, h)
	}
}

func TestTermEmulator_Reset(t *testing.T) {
	e := NewTermEmulator(2, 6)
	e.Process("\x1b[1;1H\x1b[1mstyled")
	e.Reset()

	if got := e.Visible(0); got != "" {
		t.Errorf("row 0 after reset = %q, want blank", got)
	}
	if r, c := e.Cursor(); r != 0 || c != 0 {
		t.Errorf("cursor = %d,%d, want home", r, c)
	}
	if len(e.Scrollback()) != 0 {
		t.Errorf("scrollback survived reset: %v", e.Scrollback())
	}
	// Attributes must be cleared too, or the next cell inherits bold.
	e.Process("X")
	if got := e.Cell(0, 0).Flags; got != 0 {
		t.Errorf("flags after reset = %s", got)
	}
}
