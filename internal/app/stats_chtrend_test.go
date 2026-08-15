// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// TestHandleTokenStats_CacheHitTrends verifies the pi-style per-completion
// rate and the cumulative rate both evolve per provider round: each round
// with cache activity shifts the current value into the previous baseline
// (for delta coloring) and records the new one.
func TestHandleTokenStats_CacheHitTrends(t *testing.T) {
	feed := func(a *App, promptN, cacheRead, cacheWrite int) {
		a.turnCount++ // a new turn each feed: bypass the duplicate-stats dedupe
		a.handleTokenStats(&agentic.OutputEvent{
			Timings: &agentic.TokenTimings{
				PromptN:          promptN,
				CacheReadTokens:  cacheRead,
				CacheWriteTokens: cacheWrite,
			},
		})
	}

	t.Run("per-completion rate from the round's own numbers", func(t *testing.T) {
		a := New(testSubsystems())
		// Anthropic-style: read 300 / (300 read + 100 write) = 75%.
		feed(a, 0, 300, 100)
		if !a.lastCacheHit.Seen || a.lastCacheHit.Pct != 75 {
			t.Errorf("lastCacheHit = %+v, want Pct=75 Seen=true", a.lastCacheHit)
		}
		if a.lastCacheHit.HasPrev {
			t.Errorf("first observation must have no baseline, got %+v", a.lastCacheHit)
		}
		// Second round: read 100 / (100 + 100) = 50% — previous 75 becomes baseline.
		feed(a, 0, 100, 100)
		if a.lastCacheHit.Pct != 50 || !a.lastCacheHit.HasPrev || a.lastCacheHit.PrevPct != 75 {
			t.Errorf("lastCacheHit = %+v, want Pct=50 PrevPct=75 HasPrev=true", a.lastCacheHit)
		}
	})

	t.Run("cumulative rate folds rounds together", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 0, 300, 100) // cum: 300/400 = 75%
		feed(a, 0, 100, 100) // cum: 400/600 = 66.67%
		want := float64(400) / float64(600) * 100
		if !a.cacheHit.Seen || a.cacheHit.Pct < want-0.01 || a.cacheHit.Pct > want+0.01 {
			t.Errorf("cacheHit = %+v, want Pct≈%.2f", a.cacheHit, want)
		}
		if !a.cacheHit.HasPrev || a.cacheHit.PrevPct != 75 {
			t.Errorf("cacheHit = %+v, want PrevPct=75 HasPrev=true", a.cacheHit)
		}
	})

	t.Run("cache-less rounds do not pollute the trends", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 0, 300, 100) // 75%
		feed(a, 500, 0, 0)   // cache-less round — trends untouched
		if a.lastCacheHit.Pct != 75 || a.cacheHit.Pct != 75 {
			t.Errorf("trends = %+v / %+v, want both at 75 (cache-less round skipped)", a.lastCacheHit, a.cacheHit)
		}
	})

	t.Run("trends reset on clearStats", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 0, 300, 100)
		a.clearStats()
		if a.cacheHit.Seen || a.lastCacheHit.Seen {
			t.Errorf("clearStats must reset trends, got %+v / %+v", a.cacheHit, a.lastCacheHit)
		}
	})

	t.Run("trends surface in footer stats", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 0, 300, 100)
		a.statsMu.Lock()
		st := a.buildFooterStatsLocked()
		a.statsMu.Unlock()
		if !st.CacheHit.Seen || st.CacheHit.Pct != 75 {
			t.Errorf("sessionStats.CacheHit = %+v, want Pct=75 Seen=true", st.CacheHit)
		}
		if !st.LastCacheHit.Seen || st.LastCacheHit.Pct != 75 {
			t.Errorf("sessionStats.LastCacheHit = %+v, want Pct=75 Seen=true", st.LastCacheHit)
		}
	})
}
