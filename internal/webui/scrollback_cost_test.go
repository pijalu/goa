// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"fmt"
	"runtime"
	"testing"
)

// These benchmarks are the regression detector for the transcript cost: they
// measure the STEADY-STATE cost of one frame while a long session scrolls, which
// must not depend on how much history exists. The original implementation
// deep-copied the whole transcript per frame, so a session with 20 000 rows of
// history paid 12.5 ms and 190 MB of allocation per frame; every history size
// below must land in the same ballpark.

// scrollFeed writes the next few lines a streaming session would produce and
// returns nothing: it exists so every benchmark pays the same emulator cost and
// the difference between history sizes is only the transcript work.
func scrollFeed(g *CellGrid, n int) {
	for i := 0; i < n; i++ {
		g.Process("streaming line that scrolls the transcript\r\n")
	}
}

// BenchmarkTakeScrollback measures shipping the rows that scrolled since the
// last frame, with a long transcript behind the screen.
func BenchmarkTakeScrollback(b *testing.B) {
	for _, rows := range []int{0, 100, 1000, 5000, 20000, 200000} {
		b.Run(fmt.Sprintf("history=%d", rows), func(b *testing.B) {
			g := NewCellGrid(120, 40)
			feedHistory(g, rows)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				scrollFeed(g, 2)
				g.TakeScrollback()
			}
		})
	}
}

// BenchmarkPublishCost measures the whole per-frame server-side web cost
// (parse + diff + runs + transcript) with a long transcript behind the screen.
func BenchmarkPublishCost(b *testing.B) {
	for _, rows := range []int{0, 1000, 20000, 200000} {
		b.Run(fmt.Sprintf("history=%d", rows), func(b *testing.B) {
			g := NewCellGrid(120, 40)
			feedHistory(g, rows)
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				scrollFeed(g, 2)
				_ = g.Patches()
				_ = g.TakeScrollback()
			}
		})
	}
}

// maxFrameAlloc bounds what one frame may allocate with a 120×40 screen. The
// real figure is ~430 KB (every row of a scrolled screen is copied into the diff
// baseline); the bound is deliberately loose so it measures growth, not tuning.
const maxFrameAlloc = 1 << 20

// framePatches runs one frame and reports its row count, failing if the frame
// ships a row the screen does not have.
func framePatches(t *testing.T, g *CellGrid) int {
	t.Helper()
	patches := g.Patches()
	_, rows := g.Size()
	if len(patches) > rows {
		t.Fatalf("frame shipped %d rows for a %d-row screen", len(patches), rows)
	}
	for _, p := range patches {
		if p.Row < 0 || p.Row >= rows {
			t.Fatalf("frame shipped row %d of a %d-row screen", p.Row, rows)
		}
	}
	return len(patches)
}

// TestFrameCostIsBoundedByTheScreen is the regression detector for the dirty
// backlog. The rows marked dirty since the last frame used to be a list of every
// mark taken, so the first frame after a burst of output paid for the whole
// burst: 200 000 rows printed with no frame in between meant a 2.4 ms and
// 18.8 MB frame, spent sorting and collapsing marks for at most maxRows distinct
// rows. A frame must now cost the same whether one line or a whole transcript
// arrived since the last one.
func TestFrameCostIsBoundedByTheScreen(t *testing.T) {
	for _, burst := range []int{100, 20000, 200000} {
		g := NewCellGrid(120, 40)
		feedHistory(g, burst)
		if len(g.pending) != g.rows {
			t.Fatalf("a %d-row burst left %d pending entries: the dirty set must be one flag per screen row (%d)",
				burst, len(g.pending), g.rows)
		}
		var before, after runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&before)
		shipped := framePatches(t, g)
		runtime.ReadMemStats(&after)
		if alloc := after.TotalAlloc - before.TotalAlloc; alloc > maxFrameAlloc {
			t.Fatalf("a frame after a %d-row burst allocated %d bytes (budget %d), shipping %d rows",
				burst, alloc, maxFrameAlloc, shipped)
		}
	}
}

// TestResizeRebasesTheDirtySet pins the dirty set to the geometry: a resize
// re-sizes it and asks for a repaint, and no row of the old screen can ever be
// shipped against the new one.
func TestResizeRebasesTheDirtySet(t *testing.T) {
	g := NewCellGrid(120, 40)
	feedHistory(g, 500)
	for _, size := range [][2]int{{80, 10}, {120, 40}, {60, 200}} {
		g.Resize(size[0], size[1])
		if len(g.pending) != size[1] {
			t.Fatalf("after resize to %dx%d the dirty set holds %d entries", size[0], size[1], len(g.pending))
		}
		if shipped := framePatches(t, g); shipped == 0 && size[1] > 0 {
			t.Fatalf("resize to %dx%d shipped no rows: a geometry change is a repaint", size[0], size[1])
		}
	}
}

// feedHistory scrolls n rows into the transcript. Beyond the emulator's
// retention cap the transcript stays bounded, which is the point: a long session
// must cost the same as a capped one.
func feedHistory(g *CellGrid, n int) {
	for i := 0; i < n; i++ {
		g.Process(fmt.Sprintf("history line %d\r\n", i))
	}
	// Drain what accumulated so the benchmark measures the steady state, not
	// the one-off backlog.
	g.TakeScrollback()
}
