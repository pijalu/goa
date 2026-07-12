// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"testing"
	"time"
)

// microAgent builds an Agent wired for micro compaction with a large history
// that is over the MinContextRatio but under the hard ceiling (95%). Such a
// history would be truncated by the old, non-cache-aware code; the cache-aware
// gate must defer it while the provider cache is presumed hot.
func microAgent(lastTurnEnd time.Time) *Agent {
	a := &Agent{
		cfg: Config{
			ContextCompression: ContextCompressionConfig{
				MaxTokens: 8000, // ~47% usage: over MinContextRatio (0.0) but under the 0.95 ceiling
				MicroCompaction: MicroCompactionConfig{
					KeepRecentMessages: 4,
					MinContentTokens:   1,
					MinContextRatio:    0.0, // ratio gate always passes
					TruncatedMarker:    "[cleared]",
					CacheMissThreshold: 1 * time.Hour,
				},
			},
		},
		history:     historyWithNToolResults(30, 500),
		lastTurnEnd: lastTurnEnd,
	}
	return a
}

func anyTruncated(a *Agent) bool {
	for _, m := range a.history {
		if m.Role == ToolRole && m.Content == "[cleared]" {
			return true
		}
	}
	return false
}

// TestMicroCompact_DeferredWhenCacheHot verifies the cache-aware gate: a
// proactive micro compaction must NOT mutate history while the provider cache
// is presumed hot (short inter-turn idle) and usage is below the hard ceiling.
func TestMicroCompact_DeferredWhenCacheHot(t *testing.T) {
	a := microAgent(time.Now()) // idle << 1h => hot
	a.microCompactForced(false)
	if anyTruncated(a) {
		t.Fatalf("micro compaction mutated history while cache presumed hot; prefix cache would be churned")
	}
}

// TestMicroCompact_RunsWhenCacheCold verifies that after the idle gap exceeds
// CacheMissThreshold the cache is presumed cold and the (now-safe) mutation runs.
func TestMicroCompact_RunsWhenCacheCold(t *testing.T) {
	a := microAgent(time.Now().Add(-2 * time.Hour)) // idle > 1h => cold
	a.microCompactForced(false)
	if !anyTruncated(a) {
		t.Fatalf("micro compaction did not run despite cold cache")
	}
}

// TestMicroCompact_RunsWhenForcedEvenIfHot verifies a manual /compress
// (force=true) always mutates, regardless of cache state.
func TestMicroCompact_RunsWhenForcedEvenIfHot(t *testing.T) {
	a := microAgent(time.Now()) // hot
	a.microCompactForced(true)
	if !anyTruncated(a) {
		t.Fatalf("forced micro compaction did not run")
	}
}

// TestMicroCompact_RunsAtHardCeilingEvenIfHot verifies the overflow safety
// override: at/above 95% usage the mutation runs even with a hot cache,
// because not mutating risks an overflow.
func TestMicroCompact_RunsAtHardCeilingEvenIfHot(t *testing.T) {
	a := microAgent(time.Now()) // hot
	// Shrink MaxTokens so usage is >= 95% (ceiling override).
	a.cfg.ContextCompression.MaxTokens = 3900 // ~96% usage
	a.microCompactForced(false)
	if !anyTruncated(a) {
		t.Fatalf("micro compaction did not run at the hard ceiling despite hot cache")
	}
}

// TestMicroCompact_DisabledThresholdRunsImmediately verifies the legacy path:
// CacheMissThreshold <= 0 disables cache protection, so proactive compaction
// mutates as soon as the ratio gate passes (back-compat for non-micro
// strategies where MicroCompaction stays zero).
func TestMicroCompact_DisabledThresholdRunsImmediately(t *testing.T) {
	a := microAgent(time.Now()) // hot, but threshold disabled below
	a.cfg.ContextCompression.MicroCompaction.CacheMissThreshold = 0
	a.microCompactForced(false)
	if !anyTruncated(a) {
		t.Fatalf("micro compaction did not run with cache protection disabled")
	}
}

func TestCacheAssumedCold(t *testing.T) {
	a := &Agent{
		cfg: Config{
			ContextCompression: ContextCompressionConfig{
				MicroCompaction: MicroCompactionConfig{CacheMissThreshold: 1 * time.Hour},
			},
		},
	}
	if !a.cacheAssumedCold() {
		t.Fatalf("first turn (zero lastTurnEnd) should be treated as cold")
	}
	a.lastTurnEnd = time.Now()
	if a.cacheAssumedCold() {
		t.Fatalf("recent activity with 1h threshold should be treated as hot")
	}
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour)
	if !a.cacheAssumedCold() {
		t.Fatalf("idle > threshold should be treated as cold")
	}
}
