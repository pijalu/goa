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

// TestCacheHitTrend_WeightedGlobal verifies the token-weighted
// session-wide level — the CH segment's 1st value. Rounds fold by cached
// token volume (report formula), not by count.
func TestCacheHitTrend_WeightedGlobal(t *testing.T) {
	t.Run("folds rounds by cached-token weight", func(t *testing.T) {
		a := New(testSubsystems())
		feedCacheRound(a, 0, 300, 100) // 75%, w = 300+100 = 400
		if a.lastCacheHit.GlobalPct != 75 {
			t.Errorf("GlobalPct after first round = %.1f, want 75", a.lastCacheHit.GlobalPct)
		}
		if a.lastCacheHit.GlobalHasPrev || a.cacheHitGlobalWeight != 400 {
			t.Errorf("first fold must have no baseline: hasPrev=%v weight=%d", a.lastCacheHit.GlobalHasPrev, a.cacheHitGlobalWeight)
		}
		feedCacheRound(a, 0, 100, 100) // 50%, w = 200
		want := (75.0*400 + 50.0*200) / 600
		if diff := absDiff(a.lastCacheHit.GlobalPct, want); diff > 1e-9 {
			t.Errorf("GlobalPct = %.4f, want %.4f", a.lastCacheHit.GlobalPct, want)
		}
		if !a.lastCacheHit.GlobalHasPrev || a.lastCacheHit.GlobalPrevPct != 75 {
			t.Errorf("baseline = hasPrev=%v prev=%.1f, want true/75", a.lastCacheHit.GlobalHasPrev, a.lastCacheHit.GlobalPrevPct)
		}
	})

	t.Run("report example: 10k miss + 5k full hit weighs to 33.3%", func(t *testing.T) {
		a := New(testSubsystems())
		feedCacheRound(a, 10000, 0, 0) // full miss: 0%, w = promptN = 10000
		feedCacheRound(a, 0, 5000, 0)  // full hit: 100%, w = read = 5000
		want := (0.0*10000 + 100.0*5000) / 15000
		if diff := absDiff(a.lastCacheHit.GlobalPct, want); diff > 1e-9 {
			t.Errorf("GlobalPct = %.4f, want %.4f (weighted, not the 50%% count-average)", a.lastCacheHit.GlobalPct, want)
		}
	})
}

// TestCacheHitTrend_WeightedGlobal_NoOps locks the boundary conditions of
// the weighted fold: empty rounds change nothing.
func TestCacheHitTrend_WeightedGlobal_NoOps(t *testing.T) {
	a := New(testSubsystems())
	feedCacheRound(a, 0, 300, 100)
	a.turnCount++
	a.handleTokenStats(&agentic.OutputEvent{Timings: &agentic.TokenTimings{}})
	if a.cacheHitGlobalWeight != 400 {
		t.Errorf("weight = %d, want 400 (empty round skipped)", a.cacheHitGlobalWeight)
	}
}

// TestCacheHitTrend_WeightedGlobal_Lifecycle locks the accumulator plumbing:
// clearStats resets the weighted level, and the level surfaces in the
// footer's session stats.
func TestCacheHitTrend_WeightedGlobal_Lifecycle(t *testing.T) {
	a := New(testSubsystems())
	feedCacheRound(a, 0, 300, 100)
	a.clearStats()
	if a.cacheHitGlobalLevel != 0 || a.cacheHitGlobalWeight != 0 || a.lastCacheHit.GlobalHasPrev {
		t.Errorf("clearStats must reset global accumulators, got level=%v weight=%d trend=%+v",
			a.cacheHitGlobalLevel, a.cacheHitGlobalWeight, a.lastCacheHit)
	}

	feedCacheRound(a, 0, 300, 100)
	a.turnCount++ // a post-clear round is a genuinely NEW round: advance past
	// the pre-clear turn number or handleTokenStats' duplicate guard would
	// skip the identical fingerprint (same turn number + same values).
	feedCacheRound(a, 0, 300, 100)
	a.statsMu.Lock()
	st := a.buildFooterStatsLocked()
	a.statsMu.Unlock()
	if st.LastCacheHit.GlobalPct != 75 {
		t.Errorf("sessionStats.LastCacheHit.GlobalPct = %v, want 75", st.LastCacheHit.GlobalPct)
	}
}

// TestFoldCacheHitGlobal_OpenAIDenominatorWeight pins bugs.md 2026-08-30
// (token-size weighting): an OpenAI-style round (write==0) must weight by
// its FULL CacheHitPct denominator — the cached prefix PLUS the uncached
// prompt it was computed over — not by the cached tokens alone. A bust
// round reading only 500 of 10500 prompt-side tokens otherwise counts
// ~20x too light and inflates the session-wide level (the live footer
// showed 72.6% where the honest token-weighted rate was 41.88%).
func TestFoldCacheHitGlobal_OpenAIDenominatorWeight(t *testing.T) {
	a := New(testSubsystems())
	feedCacheRound(a, 0, 300, 100)   // anthropic-style: 75%, w = 300+100 = 400
	feedCacheRound(a, 10000, 500, 0) // openai-style: 500/10500, w = 500+10000 = 10500
	// The fold must equal Σread / Σdenominator = (300+500)/(400+10500).
	want := 800.0 / 10900.0 * 100
	if diff := absDiff(a.lastCacheHit.GlobalPct, want); diff > 1e-9 {
		t.Errorf("GlobalPct = %.4f, want %.4f (Σread/Σdenominator)", a.lastCacheHit.GlobalPct, want)
	}
	if a.cacheHitGlobalWeight != 10900 {
		t.Errorf("weight = %d, want 10900 (400 + read+prompt of the openai-style round)", a.cacheHitGlobalWeight)
	}
}

// absDiff returns |a-b| for float comparisons without importing math for a
// single helper in each test binary.
func absDiff(a, b float64) float64 {
	if a < b {
		return b - a
	}
	return a - b
}
