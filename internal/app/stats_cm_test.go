// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
)

// TestHandleTokenStats_CacheMissCounter verifies the CM counter semantics
// (bugs.md CM entry): a miss is counted only when a request reads ZERO cache
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
		feed(a, 0)   // bust 2
		feed(a, 60)  // cache back — no miss
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
// session-export finding (bugs.md): an in-place history mutation (micro
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
// from the CM:13 session export (bugs.md "Provider prefix-cache bust loop"):
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

// TestBuildFooterStatParts_CacheMiss verifies CM renders next to CH only
// when non-zero.
func TestBuildFooterStatParts_CacheMiss(t *testing.T) {
	base := sessionStats{CacheReadTotal: 900, CacheWriteTotal: 100, PromptN: 100}

	withMisses := base
	withMisses.CacheMisses = 3
	joined := ansi.Strip(strings.Join(buildFooterStatParts(withMisses), " "))
	if !strings.Contains(joined, "CM:3") {
		t.Errorf("parts %q missing CM:3", joined)
	}
	chIdx := strings.Index(joined, "CH")
	cmIdx := strings.Index(joined, "CM:")
	if chIdx < 0 || cmIdx < 0 || cmIdx < chIdx {
		t.Errorf("CM must render next to (after) CH: %q", joined)
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
