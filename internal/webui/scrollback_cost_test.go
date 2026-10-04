// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"fmt"
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
	for _, rows := range []int{0, 100, 1000, 5000, 20000} {
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
	for _, rows := range []int{0, 1000, 20000} {
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
