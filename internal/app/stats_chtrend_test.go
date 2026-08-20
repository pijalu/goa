// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// feedCacheRound feeds one round of token stats with cache activity into the
// app, advancing the turn counter to bypass the duplicate-stats dedupe.
func feedCacheRound(a *App, promptN, cacheRead, cacheWrite int) {
	a.turnCount++
	a.handleTokenStats(&agentic.OutputEvent{
		Timings: &agentic.TokenTimings{
			PromptN:          promptN,
			CacheReadTokens:  cacheRead,
			CacheWriteTokens: cacheWrite,
		},
	})
}

// TestHandleTokenStats_CacheHitTrend verifies the pi-style per-completion
// rate evolves per provider round: each round with cache activity shifts the
// current value into the previous baseline (for delta coloring) and records
// the new one.
func TestHandleTokenStats_CacheHitTrend(t *testing.T) {
	t.Run("per-completion rate from the round's own numbers", func(t *testing.T) {
		a := New(testSubsystems())
		feedCacheRound(a, 0, 300, 100) // 75%
		if !a.lastCacheHit.Seen || a.lastCacheHit.Pct != 75 {
			t.Errorf("lastCacheHit = %+v, want Pct=75 Seen=true", a.lastCacheHit)
		}
		if a.lastCacheHit.HasPrev {
			t.Errorf("first observation must have no baseline, got %+v", a.lastCacheHit)
		}
		feedCacheRound(a, 0, 100, 100) // 50%
		if a.lastCacheHit.Pct != 50 || !a.lastCacheHit.HasPrev || a.lastCacheHit.PrevPct != 75 {
			t.Errorf("lastCacheHit = %+v, want Pct=50 PrevPct=75 HasPrev=true", a.lastCacheHit)
		}
	})

	t.Run("cache-less rounds do not pollute the trend", func(t *testing.T) {
		a := New(testSubsystems())
		feedCacheRound(a, 0, 300, 100) // 75%
		feedCacheRound(a, 500, 0, 0)   // cache-less round — trend untouched
		if a.lastCacheHit.Pct != 75 {
			t.Errorf("trend = %+v, want 75 (cache-less round skipped)", a.lastCacheHit)
		}
	})
}

// TestCacheHitTrend_Session verifies cumulative totals, clearStats reset,
// and footer-stats plumbing.
func TestCacheHitTrend_Session(t *testing.T) {
	t.Run("cumulative totals still fold rounds together", func(t *testing.T) {
		a := New(testSubsystems())
		feedCacheRound(a, 0, 300, 100) // totals: 300 read / 100 write
		feedCacheRound(a, 0, 100, 100) // totals: 400 read / 200 write
		if a.tokenCacheReadTotal != 400 || a.tokenCacheWriteTotal != 200 {
			t.Errorf("totals = %d/%d, want 400/200", a.tokenCacheReadTotal, a.tokenCacheWriteTotal)
		}
		if a.lastCacheHit.Pct != 50 {
			t.Errorf("lastCacheHit = %+v, want Pct=50 (per-completion rate of the last round)", a.lastCacheHit)
		}
	})

	t.Run("trend resets on clearStats", func(t *testing.T) {
		a := New(testSubsystems())
		feedCacheRound(a, 0, 300, 100)
		a.clearStats()
		if a.lastCacheHit.Seen {
			t.Errorf("clearStats must reset the trend, got %+v", a.lastCacheHit)
		}
	})

	t.Run("trend surfaces in footer stats", func(t *testing.T) {
		a := New(testSubsystems())
		feedCacheRound(a, 0, 300, 100)
		a.statsMu.Lock()
		st := a.buildFooterStatsLocked()
		a.statsMu.Unlock()
		if !st.LastCacheHit.Seen || st.LastCacheHit.Pct != 75 {
			t.Errorf("sessionStats.LastCacheHit = %+v, want Pct=75 Seen=true", st.LastCacheHit)
		}
	})
}

// TestCacheHitTrend_RollingWindow verifies the rolling average of the last
// cacheHitWindowSize rates used for the CH:<avg>% footer segment.
func TestCacheHitTrend_RollingWindow(t *testing.T) {
	t.Run("tracks last N rates", func(t *testing.T) {
		a := New(testSubsystems())
		feedCacheRound(a, 0, 300, 100) // 75%
		feedCacheRound(a, 0, 100, 100) // 50%
		feedCacheRound(a, 0, 50, 150)  // 25%
		avg := a.lastCacheHit.AvgPct()
		want := (75.0 + 50.0 + 25.0) / 3
		if avg != want {
			t.Errorf("AvgPct = %.1f, want %.1f", avg, want)
		}
	})

	t.Run("caps at cacheHitWindowSize", func(t *testing.T) {
		a := New(testSubsystems())
		for i := 0; i < 12; i++ {
			feedCacheRound(a, 0, 400, 100) // 80%
		}
		feedCacheRound(a, 0, 20, 80) // 20%
		if len(a.lastCacheHit.window) != cacheHitWindowSize {
			t.Errorf("window len = %d, want %d", len(a.lastCacheHit.window), cacheHitWindowSize)
		}
		avg := a.lastCacheHit.AvgPct()
		want := (9*80.0 + 20.0) / 10
		if avg != want {
			t.Errorf("AvgPct = %.1f, want %.1f", avg, want)
		}
	})
}

// TestCacheHitTrend_AvgPrevPct verifies the previous-baseline average used
// for delta coloring the CH:<avg>% element.
func TestCacheHitTrend_AvgPrevPct(t *testing.T) {
	t.Run("excludes latest", func(t *testing.T) {
		a := New(testSubsystems())
		feedCacheRound(a, 0, 300, 100) // 75%
		feedCacheRound(a, 0, 100, 100) // 50%
		feedCacheRound(a, 0, 50, 150)  // 25%
		avgPrev := a.lastCacheHit.AvgPrevPct()
		want := (75.0 + 50.0) / 2
		if avgPrev != want {
			t.Errorf("AvgPrevPct = %.1f, want %.1f", avgPrev, want)
		}
	})

	t.Run("single observation returns 0", func(t *testing.T) {
		a := New(testSubsystems())
		feedCacheRound(a, 0, 300, 100)
		if a.lastCacheHit.AvgPrevPct() != 0 {
			t.Errorf("AvgPrevPct with 1 obs = %.1f, want 0", a.lastCacheHit.AvgPrevPct())
		}
	})

	t.Run("window resets on clearStats", func(t *testing.T) {
		a := New(testSubsystems())
		feedCacheRound(a, 0, 300, 100)
		feedCacheRound(a, 0, 100, 100)
		a.clearStats()
		if len(a.lastCacheHit.window) != 0 {
			t.Errorf("clearStats must reset the window, got %d entries", len(a.lastCacheHit.window))
		}
		if a.lastCacheHit.AvgPct() != 0 {
			t.Errorf("AvgPct after clear = %.1f, want 0", a.lastCacheHit.AvgPct())
		}
	})
}
