// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
)

// TestHandleTokenStats_CacheMissCounter verifies the CM counter semantics
// (CM entry): a miss is counted only when a request reads ZERO cache
// tokens AFTER the cache was established; cold starts and cache-less
// providers never count.
func TestHandleTokenStats_CacheMissCounter(t *testing.T) {
	feed := func(a *App, cacheRead int) {
		a.handleTokenStats(&agentic.OutputEvent{
			Timings: &agentic.TokenTimings{CacheReadTokens: cacheRead},
		})
	}

	t.Run("misses counted only after establishment", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 100) // establishes the cache
		feed(a, 80)  // normal hit — no miss
		feed(a, 0)   // bust 1
		// Bust 2 arrives in a NEW turn (turnCount advanced): the per-turn
		// duplicate-stats dedupe (stats_dedup_test.go) otherwise collapses two
		// byte-identical all-zero emissions into one, which is the re-emission
		// artifact that guard exists to remove. Distinct-turn busts still count.
		a.turnCount++
		feed(a, 0)  // bust 2 (new turn)
		feed(a, 60) // cache back — no miss
		if a.tokenCacheMisses != 2 {
			t.Errorf("tokenCacheMisses = %d, want 2", a.tokenCacheMisses)
		}
	})

	t.Run("cold start is not a miss", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 0) // first request, cache not yet established
		feed(a, 0)
		if a.tokenCacheMisses != 0 {
			t.Errorf("tokenCacheMisses = %d, want 0 (cold start is not a bust)", a.tokenCacheMisses)
		}
	})

	t.Run("cache-less provider stays hidden", func(t *testing.T) {
		a := New(testSubsystems())
		for i := 0; i < 5; i++ {
			feed(a, 0) // provider never reports cache tokens
		}
		if a.tokenCacheMisses != 0 {
			t.Errorf("tokenCacheMisses = %d, want 0 (no cache stats reported)", a.tokenCacheMisses)
		}
	})
}

// TestHandleTokenStats_CacheMissPartialBust is the regression test for the
// session-export finding: an in-place history mutation (micro
// compaction truncating old tool results) invalidates the provider's cached
// prefix, but the next request still reads the small unmutated head from
// cache (5,376 of ~113k tokens) — the zero-read rule never fires, so the CM
// counter under-reported (showed 1 for a session with two busts). In an
// append-only conversation cache reads grow monotonically, so any
// significant DROP is a bust. A small tolerance absorbs block-quantization
// wobble in provider reporting.
func TestHandleTokenStats_CacheMissPartialBust(t *testing.T) {
	feed := func(a *App, cacheRead int) {
		a.handleTokenStats(&agentic.OutputEvent{
			Timings: &agentic.TokenTimings{CacheReadTokens: cacheRead},
		})
	}

	t.Run("partial bust after compaction counts", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 113408) // hot steady state
		feed(a, 5376)   // bust 1: compaction truncated the prefix (partial hit)
		feed(a, 56320)  // re-warm at shrunk size — no miss
		feed(a, 68096)  // growth — no miss
		feed(a, 0)      // bust 2: provider TTL expiry after idle gap
		feed(a, 70144)  // re-warm — no miss
		if a.tokenCacheMisses != 2 {
			t.Errorf("tokenCacheMisses = %d, want 2 (partial bust + TTL expiry)", a.tokenCacheMisses)
		}
	})

	t.Run("small dips within tolerance are not busts", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 113408)
		feed(a, 112500) // dip of 908 tokens < tolerance — quantization wobble
		feed(a, 113900) // growth resumes
		if a.tokenCacheMisses != 0 {
			t.Errorf("tokenCacheMisses = %d, want 0 (dip within tolerance)", a.tokenCacheMisses)
		}
	})
}

// TestHandleTokenStats_CMReplaySessionExport replays the cache_read series
// from the CM:13 session export (Provider prefix-cache bust loop):
// the round-17 anomaly drop (30592→7552), the first elision pass
// (169088→7552), and the advancing-floor busts (re-warm to ~164k, drop to an
// advancing frontier) must count EXACTLY the 13 partial-drop misses the entry
// recorded — the fix changes how often elision fires, not the detector. The
// companion convergence test in internal/agentic
// (TestMaybeCompress_ToolElision_CacheBustConvergence) covers the post-fix
// "same workload yields ≤2 misses" bar.
func TestHandleTokenStats_CMReplaySessionExport(t *testing.T) {
	feed := func(a *App, cacheRead int) {
		a.handleTokenStats(&agentic.OutputEvent{
			Timings: &agentic.TokenTimings{CacheReadTokens: cacheRead},
		})
	}

	series := []int{
		30592,  // rounds 15-16: cache established
		7552,   // round 17 anomaly drop — miss 1
		169088, // growth re-warm — no miss
		7552,   // round 141: first elision pass — miss 2
	}
	// Rounds 227-410: re-warm to ~164k between busts, then a drop to the
	// advancing elision frontier — misses 3..13.
	floors := []int{65536, 98304, 113792, 131072, 141312, 147072, 151552, 155520, 157056, 157824, 159488}
	for i, f := range floors {
		series = append(series, 164000+i*200, f)
	}

	a := New(testSubsystems())
	for _, v := range series {
		feed(a, v)
	}
	if a.tokenCacheMisses != 13 {
		t.Errorf("tokenCacheMisses = %d, want exactly 13 (the session export's CM count)", a.tokenCacheMisses)
	}
}

// TestHandleTokenStats_CacheMissFreshContextReset is the regression test for
// the fresh-context goal CM bug ("Fresh-context goal start counted as
// a cache miss"): when a goal begins on a clean context, RunFresh clears the
// live history AND rotates the provider cache key, so the first request of
// the new conversation is cold by nature — zero or tiny cache reads on a
// fresh key. The detector must re-arm on EventContextReset: the fresh
// conversation's cold start is not a bust, the session totals (CH/CW) and
// the CM counter itself survive, and a real bust after the fresh cache
// re-establishes still counts.
func TestHandleTokenStats_CacheMissFreshContextReset(t *testing.T) {
	feed := func(a *App, cacheRead int) {
		a.handleTokenStats(&agentic.OutputEvent{
			Timings: &agentic.TokenTimings{CacheReadTokens: cacheRead},
		})
	}
	reset := func(a *App) {
		a.handleAgentOutputEvent(&agentic.OutputEvent{Type: agentic.EventContextReset})
	}

	t.Run("cold start after context reset is not a miss", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 150000) // prior conversation: hot cache established
		feed(a, 151000) // normal hit
		reset(a)        // fresh-context goal begins: context + cache key reset
		feed(a, 0)      // first request of the fresh conversation: cold by nature
		feed(a, 0)      // provider cache still warming on the new key
		feed(a, 12000)  // system prompt + objective prefix now cached — establishes
		feed(a, 13000)  // normal growth — no miss
		if a.tokenCacheMisses != 0 {
			t.Errorf("tokenCacheMisses = %d, want 0 (fresh-context cold start is not a bust)", a.tokenCacheMisses)
		}
	})

	t.Run("drop from prior conversation baseline is not a miss", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 150000) // large established read before the reset
		reset(a)
		feed(a, 8192) // first fresh request hits only the shared system-prompt prefix:
		// a collapse vs the prior turn's 150k read, but a cold start, not a bust
		if a.tokenCacheMisses != 0 {
			t.Errorf("tokenCacheMisses = %d, want 0 (drop across the reset boundary is not a bust)", a.tokenCacheMisses)
		}
	})

	t.Run("bust after re-establishment still counts", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 150000)
		reset(a)
		feed(a, 12000) // establishes the fresh conversation's cache
		feed(a, 12500)
		feed(a, 0) // real bust (provider TTL expiry) — must count
		if a.tokenCacheMisses != 1 {
			t.Errorf("tokenCacheMisses = %d, want 1 (real bust after fresh re-establishment)", a.tokenCacheMisses)
		}
	})

	t.Run("reset keeps session totals and CM count", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 100)
		feed(a, 0) // one real bust before the reset
		if a.tokenCacheMisses != 1 {
			t.Fatalf("precondition: tokenCacheMisses = %d, want 1", a.tokenCacheMisses)
		}
		reset(a)
		feed(a, 0) // cold start of the fresh conversation — not a miss
		if a.tokenCacheMisses != 1 {
			t.Errorf("tokenCacheMisses = %d, want 1 (reset must not wipe the CM counter)", a.tokenCacheMisses)
		}
		if a.tokenCacheReadTotal != 100 {
			t.Errorf("tokenCacheReadTotal = %d, want 100 (session totals survive the reset)", a.tokenCacheReadTotal)
		}
	})
}

// TestBuildFooterStatParts_CacheMiss verifies CM renders next to the
// last-completion CH (▸) only when non-zero.
func TestBuildFooterStatParts_CacheMiss(t *testing.T) {
	base := sessionStats{CacheReadTotal: 900, CacheWriteTotal: 100, PromptN: 100,
		LastCacheHit: cacheHitTrendFromTotals(900, 100, 100)}

	withMisses := base
	withMisses.CacheMisses = 3
	joined := ansi.Strip(strings.Join(buildFooterStatParts(withMisses), " "))
	if !strings.Contains(joined, "CM:3") {
		t.Errorf("parts %q missing CM:3", joined)
	}
	chIdx := strings.Index(joined, "▸")
	cmIdx := strings.Index(joined, "CM:")
	if chIdx < 0 || cmIdx < 0 || cmIdx < chIdx {
		t.Errorf("CM must render next to (after) the CH rate: %q", joined)
	}

	noMisses := base
	joined = ansi.Strip(strings.Join(buildFooterStatParts(noMisses), " "))
	if strings.Contains(joined, "CM:") {
		t.Errorf("CM must be hidden at zero misses: %q", joined)
	}
}

// TestFormatContextUsage_NoParenthetical verifies the status bar context usage
// renders WITHOUT the auto-window / compression-layer parenthetical — the
// "(auto+micro)" suffix (and its "(auto)"/"(micro)"/"(elision)" variants) was
// removed from the status bar as noise (user request).
func TestFormatContextUsage_NoParenthetical(t *testing.T) {
	got := ansi.Strip(formatContextUsage(50, 100))
	if !strings.Contains(got, "50.0%/100") {
		t.Errorf("formatContextUsage = %q, want percentage", got)
	}
	if strings.Contains(got, "(") {
		t.Errorf("formatContextUsage = %q, want no parenthetical suffix", got)
	}
	for _, word := range []string{"auto", "micro", "elision"} {
		if strings.Contains(got, word) {
			t.Errorf("formatContextUsage = %q, want no %q label", got, word)
		}
	}
}
