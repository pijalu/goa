// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// assertTokenTotals checks the cumulative session token totals in one call.
func assertTokenTotals(t *testing.T, a *App, wantPrompt, wantPredicted, wantCacheRead int) {
	t.Helper()
	if a.tokenPromptTotal != wantPrompt {
		t.Errorf("tokenPromptTotal = %d, want %d", a.tokenPromptTotal, wantPrompt)
	}
	if a.tokenPredictedTotal != wantPredicted {
		t.Errorf("tokenPredictedTotal = %d, want %d", a.tokenPredictedTotal, wantPredicted)
	}
	if a.tokenCacheReadTotal != wantCacheRead {
		t.Errorf("tokenCacheReadTotal = %d, want %d", a.tokenCacheReadTotal, wantCacheRead)
	}
}

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
		assertTokenTotals(t, a, 1500, 120, 140000)
	})

	t.Run("distinct rounds in a turn all count", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 1500, 120, 140000, 0) // round 1
		feed(a, 1600, 95, 141000, 0)  // round 2: genuinely different usage — must count
		assertTokenTotals(t, a, 3100, 215, 281000)
	})

	t.Run("duplicate bust is not double-counted", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 1500, 120, 140000, 0) // establishes cache
		feed(a, 138000, 299, 0, 0)    // bust: cache_read 140000 -> 0 (miss 1)
		feed(a, 138000, 299, 0, 0)    // duplicate of the bust emission (still miss 1)
		if a.tokenCacheFullMisses != 1 {
			t.Errorf("tokenCacheFullMisses = %d, want 1 (duplicate bust emission must not recount)", a.tokenCacheFullMisses)
		}
		if a.tokenCacheMissedTokens != 140000 {
			t.Errorf("tokenCacheMissedTokens = %d, want 140000 (missed tokens must not recount either)", a.tokenCacheMissedTokens)
		}
	})

	t.Run("same values after a new user turn count again", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 1500, 120, 140000, 0)
		a.turnCount++                 // EventEnd: new user turn begins
		feed(a, 1500, 120, 140000, 0) // identical numbers, but a different turn — must count
		assertTokenTotals(t, a, 3000, 240, 280000)
	})
}
