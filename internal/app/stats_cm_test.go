// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"encoding/json"
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
		feed(a, 0)   // bust 1 (full)
		// Bust 2 arrives in a NEW turn (turnCount advanced): the per-turn
		// duplicate-stats dedupe (stats_dedup_test.go) otherwise collapses two
		// byte-identical all-zero emissions into one, which is the re-emission
		// artifact that guard exists to remove. Distinct-turn busts still count.
		a.turnCount++
		feed(a, 0)  // bust 2 (full, new turn)
		feed(a, 60) // cache back — no miss
		if a.tokenCacheFullMisses != 2 {
			t.Errorf("tokenCacheFullMisses = %d, want 2", a.tokenCacheFullMisses)
		}
		if a.tokenCachePartialMisses != 0 {
			t.Errorf("tokenCachePartialMisses = %d, want 0", a.tokenCachePartialMisses)
		}
		// Missed tokens: bust 1 saw prev=80 → 80, bust 2 saw prev=0 → 0.
		if a.tokenCacheMissedTokens != 80 {
			t.Errorf("tokenCacheMissedTokens = %d, want 80", a.tokenCacheMissedTokens)
		}
	})

	t.Run("cold start is not a miss", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 0) // first request, cache not yet established
		feed(a, 0)
		if a.tokenCacheFullMisses != 0 || a.tokenCachePartialMisses != 0 {
			t.Errorf("full=%d partial=%d, want 0|0 (cold start is not a bust)",
				a.tokenCacheFullMisses, a.tokenCachePartialMisses)
		}
	})

	t.Run("cache-less provider stays hidden", func(t *testing.T) {
		a := New(testSubsystems())
		for i := 0; i < 5; i++ {
			feed(a, 0) // provider never reports cache tokens
		}
		if a.tokenCacheFullMisses != 0 || a.tokenCachePartialMisses != 0 {
			t.Errorf("full=%d partial=%d, want 0|0 (no cache stats reported)",
				a.tokenCacheFullMisses, a.tokenCachePartialMisses)
		}
	})
}

// assertCMCounts checks the three CM accumulators in one call.
func assertCMCounts(t *testing.T, a *App, wantFull, wantPartial int, wantMissed int64) {
	t.Helper()
	if a.tokenCacheFullMisses != wantFull {
		t.Errorf("tokenCacheFullMisses = %d, want %d", a.tokenCacheFullMisses, wantFull)
	}
	if a.tokenCachePartialMisses != wantPartial {
		t.Errorf("tokenCachePartialMisses = %d, want %d", a.tokenCachePartialMisses, wantPartial)
	}
	if a.tokenCacheMissedTokens != wantMissed {
		t.Errorf("tokenCacheMissedTokens = %d, want %d", a.tokenCacheMissedTokens, wantMissed)
	}
}

// feedCacheRead delivers one cache_read stat in its own turn (each request
// is a distinct event, so the per-turn duplicate-emission dedupe cannot
// collapse identical consecutive readings).
func feedCacheRead(a *App, turn, cacheRead int) {
	a.turnCount = turn
	a.handleTokenStats(&agentic.OutputEvent{
		Timings: &agentic.TokenTimings{CacheReadTokens: cacheRead},
	})
}

// TestHandleTokenStats_CacheMissDetection is the table-driven spec for the
// full/partial branch: zero-read after establishment → full (missed = prev),
// drop beyond tolerance → partial (missed = prev - cacheRead), and the
// zero-read rule takes precedence so a miss never double-counts.
func TestHandleTokenStats_CacheMissDetection(t *testing.T) {
	tests := []struct {
		name        string
		series      []int // cache_read per request, in order
		wantFull    int
		wantPartial int
		wantMissed  int64
	}{
		{
			name:        "no establishment, no miss",
			series:      []int{0, 0, 0},
			wantFull:    0,
			wantPartial: 0,
			wantMissed:  0,
		},
		{
			name:        "steady hits, no miss",
			series:      []int{1000, 1100, 1200},
			wantFull:    0,
			wantPartial: 0,
			wantMissed:  0,
		},
		{
			name:        "zero read after establishment is a full miss costing prev",
			series:      []int{83421, 0},
			wantFull:    1,
			wantPartial: 0,
			wantMissed:  83421,
		},
		{
			name:        "drop beyond tolerance is a partial miss costing the delta",
			series:      []int{113408, 5376},
			wantFull:    0,
			wantPartial: 1,
			wantMissed:  113408 - 5376,
		},
		{
			name:        "dip within tolerance is not a miss",
			series:      []int{113408, 112500},
			wantFull:    0,
			wantPartial: 0,
			wantMissed:  0,
		},
		{
			name:        "consecutive zero reads count once per distinct turn value",
			series:      []int{100, 0},
			wantFull:    1,
			wantPartial: 0,
			wantMissed:  100,
		},
		{
			name:        "full then partial accumulate independently",
			series:      []int{100000, 0, 90000, 1000},
			wantFull:    1, // 100000 -> 0 (missed 100000)
			wantPartial: 1, // 90000 -> 1000 (missed 89000)
			wantMissed:  100000 + 89000,
		},
		{
			name:        "growth resets the baseline without a miss",
			series:      []int{5000, 5200, 5400, 5300},
			wantFull:    0,
			wantPartial: 0, // 5400 -> 5300 is within tolerance
			wantMissed:  0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			a := New(testSubsystems())
			for i, v := range tc.series {
				feedCacheRead(a, i, v)
			}
			assertCMCounts(t, a, tc.wantFull, tc.wantPartial, tc.wantMissed)
		})
	}
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
		feed(a, 0)      // bust 2: provider TTL expiry after idle gap (full)
		feed(a, 70144)  // re-warm — no miss
		if a.tokenCachePartialMisses != 1 {
			t.Errorf("tokenCachePartialMisses = %d, want 1 (compaction partial bust)", a.tokenCachePartialMisses)
		}
		if a.tokenCacheFullMisses != 1 {
			t.Errorf("tokenCacheFullMisses = %d, want 1 (TTL expiry full bust)", a.tokenCacheFullMisses)
		}
		// Missed tokens: partial bust lost 113408-5376, full bust lost 68096.
		want := int64(113408-5376) + 68096
		if a.tokenCacheMissedTokens != want {
			t.Errorf("tokenCacheMissedTokens = %d, want %d", a.tokenCacheMissedTokens, want)
		}
	})

	t.Run("small dips within tolerance are not busts", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 113408)
		feed(a, 112500) // dip of 908 tokens < tolerance — quantization wobble
		feed(a, 113900) // growth resumes
		if a.tokenCacheFullMisses != 0 || a.tokenCachePartialMisses != 0 {
			t.Errorf("full=%d partial=%d, want 0|0 (dip within tolerance)",
				a.tokenCacheFullMisses, a.tokenCachePartialMisses)
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
	series := []int{
		30592,  // rounds 15-16: cache established
		7552,   // round 17 anomaly drop — miss 1 (partial)
		169088, // growth re-warm — no miss
		7552,   // round 141: first elision pass — miss 2 (partial)
	}
	// Rounds 227-410: re-warm to ~164k between busts, then a drop to the
	// advancing elision frontier — misses 3..13.
	floors := []int{65536, 98304, 113792, 131072, 141312, 147072, 151552, 155520, 157056, 157824, 159488}
	for i, f := range floors {
		series = append(series, 164000+i*200, f)
	}

	a := New(testSubsystems())
	for i, v := range series {
		if i > 0 {
			a.turnCount++ // each replayed request is a distinct turn
		}
		a.handleTokenStats(&agentic.OutputEvent{
			Timings: &agentic.TokenTimings{CacheReadTokens: v},
		})
	}
	if got := a.tokenCacheFullMisses + a.tokenCachePartialMisses; got != 13 {
		t.Errorf("total misses = %d, want exactly 13 (the session export's CM count)", got)
	}
	if a.tokenCacheFullMisses != 0 {
		t.Errorf("tokenCacheFullMisses = %d, want 0 (every replayed bust kept a partial hit)", a.tokenCacheFullMisses)
	}
	if a.tokenCachePartialMisses != 13 {
		t.Errorf("tokenCachePartialMisses = %d, want 13", a.tokenCachePartialMisses)
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
		if a.tokenCacheFullMisses != 0 || a.tokenCachePartialMisses != 0 {
			t.Errorf("full=%d partial=%d, want 0|0 (fresh-context cold start is not a bust)",
				a.tokenCacheFullMisses, a.tokenCachePartialMisses)
		}
	})

	t.Run("drop from prior conversation baseline is not a miss", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 150000) // large established read before the reset
		reset(a)
		feed(a, 8192) // first fresh request hits only the shared system-prompt prefix:
		// a collapse vs the prior turn's 150k read, but a cold start, not a bust
		if a.tokenCacheFullMisses != 0 || a.tokenCachePartialMisses != 0 {
			t.Errorf("full=%d partial=%d, want 0|0 (drop across the reset boundary is not a bust)",
				a.tokenCacheFullMisses, a.tokenCachePartialMisses)
		}
	})

	t.Run("bust after re-establishment still counts", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 150000)
		reset(a)
		feed(a, 12000) // establishes the fresh conversation's cache
		feed(a, 12500)
		feed(a, 0) // real bust (provider TTL expiry) — must count as full
		if a.tokenCacheFullMisses != 1 {
			t.Errorf("tokenCacheFullMisses = %d, want 1 (real bust after fresh re-establishment)", a.tokenCacheFullMisses)
		}
	})

	t.Run("reset keeps session totals and CM count", func(t *testing.T) {
		a := New(testSubsystems())
		feed(a, 100)
		feed(a, 0) // one real full bust before the reset
		if a.tokenCacheFullMisses != 1 {
			t.Fatalf("precondition: tokenCacheFullMisses = %d, want 1", a.tokenCacheFullMisses)
		}
		reset(a)
		feed(a, 0) // cold start of the fresh conversation — not a miss
		if a.tokenCacheFullMisses != 1 {
			t.Errorf("tokenCacheFullMisses = %d, want 1 (reset must not wipe the CM counter)", a.tokenCacheFullMisses)
		}
		if a.tokenCacheReadTotal != 100 {
			t.Errorf("tokenCacheReadTotal = %d, want 100 (session totals survive the reset)", a.tokenCacheReadTotal)
		}
	})
}

// TestClearStats_ResetsCacheMissCounters verifies the user /clear path wipes
// all three CM accumulators together with the detector baseline.
func TestClearStats_ResetsCacheMissCounters(t *testing.T) {
	a := New(testSubsystems())
	feed := func(cacheRead int) {
		a.handleTokenStats(&agentic.OutputEvent{
			Timings: &agentic.TokenTimings{CacheReadTokens: cacheRead},
		})
	}
	feed(5000)
	a.turnCount++
	feed(1000) // partial bust → partial=1, missed=4000
	a.turnCount++
	feed(0) // full bust → full=1, missed += 1000
	if a.tokenCacheFullMisses != 1 || a.tokenCachePartialMisses != 1 || a.tokenCacheMissedTokens != 5000 {
		t.Fatalf("precondition: full=%d partial=%d missed=%d, want 1|1|5000",
			a.tokenCacheFullMisses, a.tokenCachePartialMisses, a.tokenCacheMissedTokens)
	}

	a.clearStats()

	if a.tokenCacheFullMisses != 0 || a.tokenCachePartialMisses != 0 || a.tokenCacheMissedTokens != 0 {
		t.Errorf("after /clear: full=%d partial=%d missed=%d, want 0|0|0",
			a.tokenCacheFullMisses, a.tokenCachePartialMisses, a.tokenCacheMissedTokens)
	}
	if a.cacheReadEstablished {
		t.Error("after /clear: cacheReadEstablished must re-arm (next cold start is not a bust)")
	}
}

// TestBuildFooterStatParts_CacheMiss verifies CM renders next to the
// last-completion CH (▸) only when at least one kind is non-zero.
func TestBuildFooterStatParts_CacheMiss(t *testing.T) {
	base := sessionStats{CacheReadTotal: 900, CacheWriteTotal: 100, PromptN: 100,
		LastCacheHit: cacheHitTrendFromTotals(900, 100, 100)}

	withMisses := base
	withMisses.CacheMissesFull = 2
	withMisses.CacheMissesPartial = 1
	withMisses.CacheMissedTokens = 45213
	joined := ansi.Strip(strings.Join(buildFooterStatParts(withMisses), " "))
	if !strings.Contains(joined, "CM:2|1") {
		t.Errorf("parts %q missing CM:2|1", joined)
	}
	// The footer shows counts only — the missed-token figure must NOT leak
	// into the segment (bugs.md: CM:1·71,424 → CM:1).
	if strings.Contains(joined, "45,213") || strings.Contains(joined, "CM:2|1·") {
		t.Errorf("CM part must not carry the token damage suffix: %q", joined)
	}
	chIdx := strings.Index(joined, "▸")
	cmIdx := strings.Index(joined, "CM:")
	if chIdx < 0 || cmIdx < 0 || cmIdx < chIdx {
		t.Errorf("CM must render next to (after) the CH rate: %q", joined)
	}

	noMisses := base
	joined = ansi.Strip(strings.Join(buildFooterStatParts(noMisses), " "))
	if strings.Contains(joined, "CM:") {
		t.Errorf("CM must be hidden when both kinds are zero: %q", joined)
	}
}

// TestFormatCacheMissPart is the golden-ANSI spec for the CM part: the
// "CM:" label and "|" separator stay in the default footer color, only the
// counts are colored (full in red, partial in warning orange), and a zero
// kind is omitted separator included (CM:3 partial-only, not CM:|3). The
// footer shows counts only — token damage lives in /stats:cache (bugs.md:
// CM:1·71,424 must render as CM:1).
func TestFormatCacheMissPart(t *testing.T) {
	red := ansi.Fg(cacheMissFullColor)
	orange := ansi.Fg(cacheMissPartialColor)

	tests := []struct {
		name    string
		full    int
		partial int
		want    string
	}{
		{
			name:    "both kinds",
			full:    2,
			partial: 3,
			want:    "CM:" + red + "2" + ansi.Reset + "|" + orange + "3" + ansi.Reset,
		},
		{
			name:    "full only omits the partial segment",
			full:    1,
			partial: 0,
			want:    "CM:" + red + "1" + ansi.Reset,
		},
		{
			name:    "partial only omits the full count and the separator",
			full:    0,
			partial: 4,
			want:    "CM:" + orange + "4" + ansi.Reset,
		},
		{
			name:    "both zero still renders the bare label (caller hides it)",
			full:    0,
			partial: 0,
			want:    "CM:",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := formatCacheMissPart(tc.full, tc.partial); got != tc.want {
				t.Errorf("formatCacheMissPart(%d, %d):\n got %q\nwant %q",
					tc.full, tc.partial, got, tc.want)
			}
		})
	}
}


// TestSessionStats_CacheMissJSONKeys pins the persisted session-summary keys
// so downstream session exports keep a stable schema.
func TestSessionStats_CacheMissJSONKeys(t *testing.T) {
	st := sessionStats{CacheMissesFull: 2, CacheMissesPartial: 3, CacheMissedTokens: 145312}
	raw, err := json.Marshal(st)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	for _, key := range []string{"cm_full", "cm_partial", "cm_tokens"} {
		if _, ok := got[key]; !ok {
			t.Errorf("marshalled sessionStats missing key %q: %s", key, raw)
		}
	}
	if got["cm_full"] != float64(2) || got["cm_partial"] != float64(3) || got["cm_tokens"] != float64(145312) {
		t.Errorf("unexpected values: %s", raw)
	}

	// Zero values are omitted (omitempty) so clean sessions carry no CM keys.
	raw, err = json.Marshal(sessionStats{})
	if err != nil {
		t.Fatalf("marshal zero: %v", err)
	}
	if strings.Contains(string(raw), "cm_") {
		t.Errorf("zero sessionStats must omit cm_* keys, got %s", raw)
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