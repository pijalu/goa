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
