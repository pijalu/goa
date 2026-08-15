// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// TestHandleTokenStats_CacheHitTrend verifies the pi-style per-completion
// rate evolves per provider round: each round with cache activity shifts the
// current value into the previous baseline (for delta coloring) and records
// the new one. The cumulative session rate is gone from the status bar —
// only this last-completion trend remains.
func TestHandleTokenStats_CacheHitTrend(t *testing.T) {
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

	t.Run("cumulative totals still fold rounds together", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 0, 300, 100) // totals: 300 read / 100 write
		feed(a, 0, 100, 100) // totals: 400 read / 200 write
		if a.tokenCacheReadTotal != 400 || a.tokenCacheWriteTotal != 200 {
			t.Errorf("totals = %d/%d, want 400/200", a.tokenCacheReadTotal, a.tokenCacheWriteTotal)
		}
		if a.lastCacheHit.Pct != 50 {
			t.Errorf("lastCacheHit = %+v, want Pct=50 (per-completion rate of the last round)", a.lastCacheHit)
		}
	})

	t.Run("cache-less rounds do not pollute the trend", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 0, 300, 100) // 75%
		feed(a, 500, 0, 0)   // cache-less round — trend untouched
		if a.lastCacheHit.Pct != 75 {
			t.Errorf("trend = %+v, want 75 (cache-less round skipped)", a.lastCacheHit)
		}
	})

	t.Run("trend resets on clearStats", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 0, 300, 100)
		a.clearStats()
		if a.lastCacheHit.Seen {
			t.Errorf("clearStats must reset the trend, got %+v", a.lastCacheHit)
		}
	})

	t.Run("trend surfaces in footer stats", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 0, 300, 100)
		a.statsMu.Lock()
		st := a.buildFooterStatsLocked()
		a.statsMu.Unlock()
		if !st.LastCacheHit.Seen || st.LastCacheHit.Pct != 75 {
			t.Errorf("sessionStats.LastCacheHit = %+v, want Pct=75 Seen=true", st.LastCacheHit)
		}
	})
}
