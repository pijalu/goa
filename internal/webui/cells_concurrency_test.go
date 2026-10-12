// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"fmt"
	"strings"
	"sync"
	"testing"
)

// CellGrid is documented as safe for concurrent use: the render loop writes
// while transports (and the no-JS page's handler goroutine) snapshot. Two
// readers may therefore drain the scrollback at the same time as the engine
// clears the screen, which is exactly the interleaving this test runs under
// -race.
func TestCellGrid_ConcurrentScrollbackAndClearAreRaceFree(t *testing.T) {
	g := NewCellGrid(20, 3)
	scrollRows(t, g, 40)

	var wg sync.WaitGroup
	for i := 0; i < 2; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for n := 0; n < 200; n++ {
				_ = g.TakeScrollback()
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		for n := 0; n < 200; n++ {
			g.Clear()
		}
	}()
	wg.Wait()
}

// Patches must cost what the changed rows cost, not what the screen costs: the
// steady state is one or two rows per frame, and re-snapshotting the whole grid
// for each of them is the difference between a lean server and a busy one.
func BenchmarkCellGridPatches(b *testing.B) {
	g := NewCellGrid(200, 60)
	// A full screen of content, so every row is non-trivial to compare.
	for r := 1; r <= 60; r++ {
		g.Process(fmt.Sprintf("\x1b[%d;1H%s", r, strings.Repeat("x", 200)))
	}
	g.Patches()

	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// One row changes per frame — the streaming case.
		g.Process(fmt.Sprintf("\x1b[30;1Hframe %d", i))
		g.Patches()
	}
}

// scrollRows feeds n line feeds through a full screen so rows land in the
// scrollback buffer.
func scrollRows(t *testing.T, g *CellGrid, n int) {
	t.Helper()
	_, rows := g.Size()
	for i := 0; i < n; i++ {
		g.Process(fmt.Sprintf("\x1b[%d;1Hrow %d\n", rows, i))
	}
}
