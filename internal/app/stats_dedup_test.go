// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// TestHandleTokenStats_DuplicateRoundStatsDeduped is the regression test for
// the 2026-08-04 session-export finding: emitTurnStats re-emits the unchanged
// providerUsage on consecutive round ends (the provider-usage path never sets
// turnStatsEmitted), so the App receives the SAME TokenTimings twice per turn.
// Accumulating both copies double-counts session totals, the DB usage record,
// and the cache-bust counter (usage.db showed ~2× inflated figures: 716 rows
// for 379 real calls). Identical stats within one turn must be recorded once.
func TestHandleTokenStats_DuplicateRoundStatsDeduped(t *testing.T) {
	feed := func(a *App, promptN, predictedN, cacheRead, cacheWrite int) {
		a.handleTokenStats(&agentic.OutputEvent{
			Timings: &agentic.TokenTimings{
				PromptN:          promptN,
				PredictedN:       predictedN,
				CacheReadTokens:  cacheRead,
				CacheWriteTokens: cacheWrite,
			},
		})
	}

	t.Run("identical repeat within a turn counts once", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 1500, 120, 140000, 0) // round end: provider usage emitted
		feed(a, 1500, 120, 140000, 0) // duplicate: same unchanged providerUsage re-emitted
		if a.tokenPromptTotal != 1500 {
			t.Errorf("tokenPromptTotal = %d, want 1500 (duplicate must not double-count)", a.tokenPromptTotal)
		}
		if a.tokenPredictedTotal != 120 {
			t.Errorf("tokenPredictedTotal = %d, want 120", a.tokenPredictedTotal)
		}
		if a.tokenCacheReadTotal != 140000 {
			t.Errorf("tokenCacheReadTotal = %d, want 140000", a.tokenCacheReadTotal)
		}
	})

	t.Run("distinct rounds in a turn all count", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 1500, 120, 140000, 0) // round 1
		feed(a, 1600, 95, 141000, 0)  // round 2: genuinely different usage — must count
		if a.tokenPromptTotal != 3100 {
			t.Errorf("tokenPromptTotal = %d, want 3100 (distinct rounds must both count)", a.tokenPromptTotal)
		}
		if a.tokenCacheReadTotal != 281000 {
			t.Errorf("tokenCacheReadTotal = %d, want 281000", a.tokenCacheReadTotal)
		}
	})

	t.Run("duplicate bust is not double-counted", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 1500, 120, 140000, 0) // establishes cache
		feed(a, 138000, 299, 0, 0)    // bust: cache_read 140000 -> 0 (miss 1)
		feed(a, 138000, 299, 0, 0)    // duplicate of the bust emission (still miss 1)
		if a.tokenCacheMisses != 1 {
			t.Errorf("tokenCacheMisses = %d, want 1 (duplicate bust emission must not recount)", a.tokenCacheMisses)
		}
	})

	t.Run("same values after a new user turn count again", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 1500, 120, 140000, 0)
		a.turnCount++                 // EventEnd: new user turn begins
		feed(a, 1500, 120, 140000, 0) // identical numbers, but a different turn — must count
		if a.tokenPromptTotal != 3000 {
			t.Errorf("tokenPromptTotal = %d, want 3000 (same values in a NEW turn must count)", a.tokenPromptTotal)
		}
	})
}
