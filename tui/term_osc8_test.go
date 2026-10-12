// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "testing"

func TestTermEmulator_OSC8HyperlinkPerCell(t *testing.T) {
	e := NewTermEmulator(1, 20)
	e.Process("\x1b]8;;https://goa.dev\x07goa\x1b]8;;\x07 plain")

	cells := e.Cells(0)
	if cells[0].Link != "https://goa.dev" {
		t.Errorf("link cell = %q, want the URI", cells[0].Link)
	}
	if cells[0].Flags&AttrLink == 0 {
		t.Errorf("link cell flags = %s, want the link bit", cells[0].Flags)
	}
	// Closing OSC 8 ends the link: the following cells are plain text and must
	// not inherit the URI (that would make the whole row clickable).
	if cells[3].Link != "" || cells[3].Flags&AttrLink != 0 {
		t.Errorf("cell after close = %+v, want no link", cells[3])
	}
	if cells[2].Link != "https://goa.dev" {
		t.Errorf("last linked cell link = %q", cells[2].Link)
	}
	// Visible text is unchanged by the link — the OSC never reaches the screen.
	if got := e.Visible(0); got[:9] != "goa plain" {
		t.Errorf("visible = %q", got[:9])
	}
}

func TestTermEmulator_OSC8WithSTTerminatorAndParams(t *testing.T) {
	e := NewTermEmulator(1, 12)
	// ST (ESC \) terminator, and an id=... parameter before the URI.
	e.Process("\x1b]8;id=7;https://example.com/a;b\x1b\\X")

	if got := e.Cell(0, 0).Link; got != "https://example.com/a;b" {
		t.Errorf("link = %q, want the full URI including its semicolon", got)
	}
}

func TestTermEmulator_OSCSplitAcrossWrites(t *testing.T) {
	e := NewTermEmulator(1, 12)
	// The compositor may deliver the OSC in two writes; the payload must not
	// leak onto the screen as printable text.
	e.Process("\x1b]8;;https://goa.dev")
	e.Process("\x07ok")

	if got := e.Visible(0); got != "ok" {
		t.Errorf("visible = %q, want %q (no OSC payload leaked)", got, "ok")
	}
	if got := e.Cell(0, 0).Link; got != "https://goa.dev" {
		t.Errorf("link = %q, want the URI", got)
	}
}

func TestTermEmulator_NonHyperlinkOSCIsIgnored(t *testing.T) {
	e := NewTermEmulator(1, 12)
	e.Process("\x1b]0;window title\x07X")

	if got := e.Visible(0); got != "X" {
		t.Errorf("visible = %q, want %q (title must not be printed)", got, "X")
	}
	if got := e.Cell(0, 0).Link; got != "" {
		t.Errorf("link = %q, want none", got)
	}
}

func TestTermEmulator_LinkClearedByEraseAndReset(t *testing.T) {
	e := NewTermEmulator(1, 8)
	e.Process("\x1b]8;;https://goa.dev\x07goa")
	e.Process("\x1b[2K")
	if got := e.Cell(0, 0).Link; got != "" {
		t.Errorf("link after erase = %q, want none", got)
	}

	e2 := NewTermEmulator(1, 8)
	e2.Process("\x1b]8;;https://goa.dev\x07goa")
	e2.Reset()
	e2.Process("X")
	if got := e2.Cell(0, 0); got.Link != "" || got.Flags != 0 {
		t.Errorf("cell after reset = %+v, want plain", got)
	}
}

func TestTermEmulator_LinkSurvivesScrollIntoScrollback(t *testing.T) {
	e := NewTermEmulator(2, 12)
	e.Process("\x1b]8;;https://goa.dev\x07goa")
	e.Process("\x1b[2;1Htwo")
	e.Process("\x1b[3;1Hthree\n")

	rows := e.ScrollbackCells()
	if len(rows) != 1 || rows[0][0].Link != "https://goa.dev" {
		t.Fatalf("scrollback rows = %+v, want the linked row", rows)
	}
}

func TestTermEmulator_LinkSurvivesResize(t *testing.T) {
	e := NewTermEmulator(2, 12)
	e.Process("\x1b]8;;https://goa.dev\x07goa")
	e.Resize(6, 2)
	if got := e.Cell(0, 0).Link; got != "https://goa.dev" {
		t.Errorf("link after shrink = %q", got)
	}
	e.Resize(20, 3)
	if got := e.Cell(0, 0).Link; got != "https://goa.dev" {
		t.Errorf("link after grow = %q", got)
	}
}

func TestTermEmulator_AttrLinkBitValue(t *testing.T) {
	// The wire bitmask is documented in specs/webui.md §20; the browser client
	// hard-codes 64, so this must never move.
	if AttrLink != 64 {
		t.Errorf("AttrLink = %d, want 64", AttrLink)
	}
	if got := (AttrBold | AttrLink).String(); got != "bold+link" {
		t.Errorf("flags string = %q", got)
	}
}
