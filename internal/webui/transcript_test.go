// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"fmt"
	"testing"

	"github.com/pijalu/goa/tui"
)

// The transcript contract. Every rule here exists because the transcript is the
// one part of the frame path that used to grow with the session: the server
// must ship each row exactly once, in absolute order, and must never do work
// proportional to how much history exists.

// maxScrollback is the emulator's retention cap, read through the tui package
// so these tests follow the cap if it ever moves.
const maxScrollback = tui.MaxScrollbackRows

// feedTranscriptScroll writes lines until at least want rows have scrolled into
// the transcript, so a test never has to reason about how many lines the screen
// itself absorbs. It stops after a bounded number of lines, which is what makes
// it usable with want beyond the retention cap (where the count can never be
// reached because eviction keeps the transcript bounded).
func feedTranscriptScroll(g *CellGrid, want int) {
	_, rows := g.Size()
	for i := 0; i < want+rows+8; i++ {
		if g.emu.ScrollbackLen() >= want {
			return
		}
		g.Process(fmt.Sprintf("line %d\r\n", i))
	}
}

// transcriptRows drains every row the grid has to ship.
func transcriptRows(g *CellGrid) []RowPatch {
	var out []RowPatch
	for {
		batch := g.TakeScrollback()
		if len(batch) == 0 {
			return out
		}
		out = append(out, batch...)
	}
}

// TestTakeScrollbackShipsOnlyNewRows pins the incremental behaviour: a second
// call must not re-ship what the first one delivered. This is the property that
// makes the per-frame cost independent of the session's length.
func TestTakeScrollbackShipsOnlyNewRows(t *testing.T) {
	g := NewCellGrid(40, 4)
	feedTranscriptScroll(g, 3)

	first := g.TakeScrollback()
	if len(first) != 3 {
		t.Fatalf("first take = %d rows, want 3", len(first))
	}
	if again := g.TakeScrollback(); len(again) != 0 {
		t.Errorf("second take repeated %d rows, want none", len(again))
	}

	feedTranscriptScroll(g, 5)
	third := g.TakeScrollback()
	if len(third) != 2 {
		t.Fatalf("take after 2 more scrolled rows = %d rows, want 2", len(third))
	}
	if want := first[len(first)-1].Row + 1; third[0].Row != want {
		t.Errorf("first new row index = %d, want %d (absolute transcript position)", third[0].Row, want)
	}
}

// TestTakeScrollbackIndicesAreContiguousAbsolutePositions is the invariant the
// client's transcript relies on: rows arrive in order, exactly once, numbered
// from the start of the session even when the server has evicted older ones.
func TestTakeScrollbackIndicesAreContiguousAbsolutePositions(t *testing.T) {
	g := NewCellGrid(40, 4)
	feedTranscriptScroll(g, maxScrollback+500)

	rows := transcriptRows(g)
	if len(rows) == 0 {
		t.Fatal("no transcript rows were shipped")
	}
	for i := 1; i < len(rows); i++ {
		if rows[i].Row != rows[i-1].Row+1 {
			t.Fatalf("row %d follows %d: indices must be contiguous", rows[i].Row, rows[i-1].Row)
		}
	}
	// The shipped window is exactly what the emulator still retains, addressed
	// by absolute index: history evicted before anyone read it is skipped, not
	// silently renumbered.
	base, end := g.emu.ScrollbackBase(), g.emu.ScrollbackBase()+g.emu.ScrollbackLen()
	if rows[0].Row != base {
		t.Errorf("oldest shipped row = %d, want %d (evicted rows are skipped)", rows[0].Row, base)
	}
	if last := rows[len(rows)-1].Row; last != end-1 {
		t.Errorf("newest shipped row = %d, want %d", last, end-1)
	}
}

// TestTakeScrollbackAfterClearRestartsTranscript covers CSI 3 / Clear: the
// transcript restarts rather than losing rows or repeating them.
func TestTakeScrollbackAfterClearRestartsTranscript(t *testing.T) {
	g := NewCellGrid(40, 4)
	feedTranscriptScroll(g, 3)
	if got := len(g.TakeScrollback()); got != 3 {
		t.Fatalf("take = %d rows, want 3", got)
	}

	g.Clear()
	feedTranscriptScroll(g, 2)
	batch := g.TakeScrollback()
	if len(batch) != 2 {
		t.Fatalf("take after clear = %d rows, want 2", len(batch))
	}
	if batch[0].Row != 0 {
		t.Errorf("first row after clear = %d, want 0", batch[0].Row)
	}
}

// TestTakeScrollbackAfterEvictionDoesNotRepeatRows is the regression test for
// the sibling failure mode of the old whole-buffer copy: a consumer that falls
// behind far enough for the retention cap to evict unread rows must resume at
// the oldest row that still exists, never re-ship one it already sent.
func TestTakeScrollbackAfterEvictionDoesNotRepeatRows(t *testing.T) {
	g := NewCellGrid(40, 4)
	feedTranscriptScroll(g, 5)
	sent := g.TakeScrollback()
	if len(sent) != 5 {
		t.Fatalf("take = %d rows, want 5", len(sent))
	}

	// Scroll far past the retention cap without taking anything.
	feedTranscriptScroll(g, maxScrollback+100)
	if g.emu.ScrollbackBase() == 0 {
		t.Fatal("nothing was evicted: the test did not reach the retention cap")
	}
	batch := g.TakeScrollback()
	if len(batch) == 0 {
		t.Fatal("no rows shipped after eviction")
	}
	if batch[0].Row < sent[len(sent)-1].Row {
		t.Errorf("resumed at row %d, before the last shipped row %d: rows would repeat",
			batch[0].Row, sent[len(sent)-1].Row)
	}
	for i := 1; i < len(batch); i++ {
		if batch[i].Row != batch[i-1].Row+1 {
			t.Fatalf("row %d follows %d: indices must be contiguous", batch[i].Row, batch[i-1].Row)
		}
	}
}

// TestTakeScrollbackCostIsIndependentOfHistory is the measured guarantee, as a
// test: the per-frame transcript cost must not scale with how much history the
// session has. It asserts the allocation budget, which is what made the old
// implementation pin a core (190 MB allocated per frame at 20 000 rows).
func TestTakeScrollbackCostIsIndependentOfHistory(t *testing.T) {
	small := NewCellGrid(120, 40)
	feedTranscriptScroll(small, 200)

	large := NewCellGrid(120, 40)
	feedTranscriptScroll(large, maxScrollback+20000)

	for _, g := range []*CellGrid{small, large} {
		g.TakeScrollback() // drain whatever is pending
	}
	smallAllocs := testing.AllocsPerRun(50, func() { small.TakeScrollback() })
	largeAllocs := testing.AllocsPerRun(50, func() { large.TakeScrollback() })

	// An empty take allocates nothing at all; both grids must be there.
	if smallAllocs != 0 {
		t.Errorf("empty take on a short transcript allocated %v times, want 0", smallAllocs)
	}
	if largeAllocs != 0 {
		t.Errorf("empty take on a long transcript allocated %v times, want 0 — "+
			"the transcript cost must not grow with history", largeAllocs)
	}
}

// TestEmulatorScrollbackIsBounded pins the retention cap and the two views of
// the transcript staying in lockstep.
func TestEmulatorScrollbackIsBounded(t *testing.T) {
	g := NewCellGrid(40, 4)
	feedTranscriptScroll(g, maxScrollback+2000)

	emu := g.emu
	if got := emu.ScrollbackLen(); got > maxScrollback {
		t.Errorf("retained %d transcript rows, want at most %d", got, maxScrollback)
	}
	if got := len(emu.Scrollback()); got != emu.ScrollbackLen() {
		t.Errorf("Scrollback() has %d rows, the cell view has %d: "+
			"the two views of the transcript must not diverge", got, emu.ScrollbackLen())
	}
	if base := emu.ScrollbackBase(); base <= 0 {
		t.Errorf("ScrollbackBase = %d after eviction, want > 0", base)
	}
}
