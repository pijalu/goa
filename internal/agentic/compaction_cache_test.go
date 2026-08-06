// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
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

// Regression test for bugs.md "Micro-compaction cache gate fails open during
// the entire first turn": lastTurnEnd is written only at turn END, so a
// zero-lastTurnEnd gate presumed cold for the WHOLE first turn — at round 58
// of turn 1 a micro compaction busted a cache that had been hot since round 2
// (cache_read 113,408 → 5,376). Once any completed request reports
// cache_read_tokens > 0 (cacheWarmObserved), the first-turn presumption must
// expire. Incident shape: first turn, ratio 53% (≥ min_context_ratio 0.5,
// below the 85% deferral ceiling) → micro compaction must DEFER.
func TestMicroCompact_FirstTurnDeferredWhenCacheWarmObserved(t *testing.T) {
	a := microAgent(time.Time{}) // first turn: no completed turn yet
	// ~53% usage: over the 0.5 ratio gate, under the 85% deferral ceiling,
	// so the cache gate — not the ratio gate or the ceiling — decides.
	a.cfg.ContextCompression.MaxTokens = 7000
	a.cfg.ContextCompression.MicroCompaction.MinContextRatio = 0.5
	// Round 1's completed request reported cache_read_tokens > 0.
	a.cacheWarmObserved = true

	a.microCompactForced(false)
	if anyTruncated(a) {
		t.Fatalf("first-turn micro compaction ran despite observed cache hits; " +
			"the hot provider prefix cache would be churned (bugs.md first-turn gate)")
	}
}

// The same first turn with NO cache hits reported (cache_read == 0 on every
// completed request) is a genuine cold cache: compaction may still run.
func TestMicroCompact_FirstTurnRunsWhenNoCacheWarmObserved(t *testing.T) {
	a := microAgent(time.Time{}) // first turn, no warm evidence
	a.cfg.ContextCompression.MaxTokens = 7000
	a.cfg.ContextCompression.MicroCompaction.MinContextRatio = 0.5

	a.microCompactForced(false)
	if !anyTruncated(a) {
		t.Fatalf("first-turn micro compaction did not run on a genuinely cold cache")
	}
}

// TestCacheAssumedCold_WarmObservation pins the gate semantics: warm evidence
// expires ONLY the first-turn (zero lastTurnEnd) presumption; the idle-gap
// TTL logic is unchanged — a warm observation must not resurrect a cache that
// sat idle past its TTL (provider TTL expiry is real, see the bugs.md note on
// the 34-min idle-gap miss). Both gates (micro + proactive) must agree.
func TestCacheAssumedCold_WarmObservation(t *testing.T) {
	newAgent := func() *Agent {
		return &Agent{
			cfg: Config{
				ContextCompression: ContextCompressionConfig{
					MicroCompaction: MicroCompactionConfig{CacheMissThreshold: 1 * time.Hour},
				},
			},
		}
	}

	// First turn, warm evidence → hot for both gates.
	a := newAgent()
	a.cacheWarmObserved = true
	if a.cacheAssumedCold() {
		t.Fatalf("first turn with observed cache hits must be treated as hot")
	}
	if a.cacheAssumedColdForProactive() {
		t.Fatalf("proactive gate must agree: first turn with cache hits is hot")
	}

	// Warm evidence + idle past the TTL → STILL cold (idle-gap logic wins).
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour)
	if !a.cacheAssumedCold() {
		t.Fatalf("idle > threshold must stay cold even with warm observation (provider TTL expiry)")
	}
	if !a.cacheAssumedColdForProactive() {
		t.Fatalf("proactive gate must agree: idle > threshold is cold despite warm observation")
	}

	// Warm evidence + recent turn end → hot (existing later-turn behavior).
	a.lastTurnEnd = time.Now()
	if a.cacheAssumedCold() {
		t.Fatalf("recent turn with 1h threshold should be treated as hot")
	}
	if a.cacheAssumedColdForProactive() {
		t.Fatalf("proactive gate must agree: recent turn is hot")
	}

	// First turn, no warm evidence → cold (genuine cold cache).
	b := newAgent()
	if !b.cacheAssumedCold() {
		t.Fatalf("first turn without observed cache hits should be treated as cold")
	}
	if !b.cacheAssumedColdForProactive() {
		t.Fatalf("proactive gate must agree: first turn without cache hits is cold")
	}
}

// TestCaptureStreamResult_SetsCacheWarmObserved verifies the observation
// hook: a completed request whose usage reports cache_read_tokens > 0 flips
// the warm flag; zero cache reads or missing usage leave it unset.
func TestCaptureStreamResult_SetsCacheWarmObserved(t *testing.T) {
	t.Run("cache read flips the flag", func(t *testing.T) {
		a := &Agent{}
		s := provider.NewAssistantMessageEventStream(4)
		s.End(&provider.AssistantMessage{
			Usage: &provider.Usage{InputTokens: 10, OutputTokens: 2, CacheReadTokens: 100},
		})
		a.captureStreamResult(s)
		if !a.cacheWarmObserved {
			t.Fatalf("cache_read_tokens=100 must set cacheWarmObserved")
		}
	})

	t.Run("zero cache read leaves the flag unset", func(t *testing.T) {
		a := &Agent{}
		s := provider.NewAssistantMessageEventStream(4)
		s.End(&provider.AssistantMessage{
			Usage: &provider.Usage{InputTokens: 10, OutputTokens: 2},
		})
		a.captureStreamResult(s)
		if a.cacheWarmObserved {
			t.Fatalf("cache_read_tokens=0 must not set cacheWarmObserved")
		}
	})

	t.Run("missing usage leaves the flag unset", func(t *testing.T) {
		a := &Agent{}
		s := provider.NewAssistantMessageEventStream(4)
		s.End(&provider.AssistantMessage{})
		a.captureStreamResult(s)
		if a.cacheWarmObserved {
			t.Fatalf("nil usage must not set cacheWarmObserved")
		}
	})
}

// TestClear_ClearsCacheWarmObserved verifies a conversation reset drops the
// warmth evidence: the new conversation's prefix is not in the provider
// cache, so the first-turn cold presumption must apply again.
func TestClear_ClearsCacheWarmObserved(t *testing.T) {
	a := &Agent{}
	a.cacheWarmObserved = true
	a.Clear()
	if a.cacheWarmObserved {
		t.Fatalf("Clear must reset cacheWarmObserved for the new conversation")
	}
}

// --- bugs.md CM:13 companion defect: mid-turn gate clock ---

// TestCacheGates_MidTurnRoundActivity is the regression for the CM:13
// companion defect: cacheAssumedCold read lastTurnEnd, written only at turn
// END — so >1h into a long single turn (the session ran ~399 rounds / ~40min
// in one turn) the idle-gap logic would flip the gate cold while rounds were
// still completing every few seconds, and the gate would fire BELOW the
// ceiling, busting a provably hot cache. Per-round activity
// (lastRoundActivity) must keep both gates hot.
func TestCacheGates_MidTurnRoundActivity(t *testing.T) {
	newAgent := func() *Agent {
		return &Agent{
			cfg: Config{
				ContextCompression: ContextCompressionConfig{
					MicroCompaction: MicroCompactionConfig{CacheMissThreshold: 1 * time.Hour},
				},
			},
		}
	}

	// Turn 1 ended >1h ago, but a round of the current (long) turn just
	// completed: both gates must read hot.
	a := newAgent()
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour)
	a.lastRoundActivity = time.Now()
	if a.cacheAssumedCold() {
		t.Fatalf("rounds completing mid-turn must keep the cache hot (stale lastTurnEnd flips it cold)")
	}
	if a.cacheAssumedColdForProactive() {
		t.Fatalf("proactive gate must agree: mid-turn round activity means hot")
	}

	// Rounds ALSO stopped >1h ago: genuinely idle → cold on both gates.
	a.lastRoundActivity = time.Now().Add(-2 * time.Hour)
	if !a.cacheAssumedCold() {
		t.Fatalf("idle past the threshold (turn and rounds both stale) must be cold")
	}
	if !a.cacheAssumedColdForProactive() {
		t.Fatalf("proactive gate must agree: stale round activity is cold")
	}
}

// TestCacheGates_FirstTurnUsageLessProviderStaysCold pins the fail-open side:
// a provider that reports no cache stats has no proven cache to protect, so
// turn-1 rounds completing must NOT flip the gate hot — otherwise the
// per-round compression gate is suppressed for the whole first turn
// (TestAgent_CompressionGateBetweenRounds shape). Warm evidence flips it hot.
func TestCacheGates_FirstTurnUsageLessProviderStaysCold(t *testing.T) {
	a := &Agent{
		cfg: Config{
			ContextCompression: ContextCompressionConfig{
				MicroCompaction: MicroCompactionConfig{CacheMissThreshold: 1 * time.Hour},
			},
		},
	}
	a.lastRoundActivity = time.Now() // rounds completing, but turn 1 + no cache reads
	if !a.cacheAssumedCold() {
		t.Fatalf("turn 1 with no cache-read evidence must fail open even while rounds complete")
	}
	if !a.cacheAssumedColdForProactive() {
		t.Fatalf("proactive gate must agree: turn 1 without warm evidence is cold")
	}
	a.cacheWarmObserved = true
	if a.cacheAssumedCold() {
		t.Fatalf("cache reads reported mid-turn-1 must flip the gate hot")
	}
	if a.cacheAssumedColdForProactive() {
		t.Fatalf("proactive gate must agree: warm evidence means hot")
	}
}

// TestMicroCompact_MidTurnDeferredWhenRoundsActive drives the companion
// defect end-to-end: a long turn past the 1h CacheMissThreshold (stale
// lastTurnEnd) with rounds still completing must NOT truncate below the
// deferral ceiling — before the fix this truncated and busted the hot cache.
func TestMicroCompact_MidTurnDeferredWhenRoundsActive(t *testing.T) {
	a := microAgent(time.Now().Add(-2 * time.Hour)) // stale inter-turn clock
	a.lastRoundActivity = time.Now()                // but rounds still completing
	a.microCompactForced(false)
	if anyTruncated(a) {
		t.Fatalf("micro compaction truncated mid-turn on a stale lastTurnEnd; " +
			"per-round activity must keep the hot cache deferred (bugs.md CM:13 companion defect)")
	}
}

// TestCaptureStreamResult_SetsLastRoundActivity verifies the per-round
// observation hook: every completed request refreshes the gate clock and the
// cache_read forensics delta, even when the provider sends no usage block.
func TestCaptureStreamResult_SetsLastRoundActivity(t *testing.T) {
	t.Run("completed request with usage", func(t *testing.T) {
		a := &Agent{}
		before := time.Now()
		s := provider.NewAssistantMessageEventStream(4)
		s.End(&provider.AssistantMessage{
			Usage: &provider.Usage{InputTokens: 10, OutputTokens: 2, CacheReadTokens: 42},
		})
		a.captureStreamResult(s)
		if a.lastRoundActivity.Before(before) {
			t.Fatalf("completed request must record lastRoundActivity")
		}
		if a.lastCacheReadTokens != 42 {
			t.Fatalf("lastCacheReadTokens = %d, want 42 (forensics delta)", a.lastCacheReadTokens)
		}
	})

	t.Run("completed request without usage", func(t *testing.T) {
		a := &Agent{}
		s := provider.NewAssistantMessageEventStream(4)
		s.End(&provider.AssistantMessage{})
		a.captureStreamResult(s)
		if a.lastRoundActivity.IsZero() {
			t.Fatalf("a completed request must record activity even without a usage block")
		}
		if a.lastCacheReadTokens != 0 {
			t.Fatalf("no usage block must leave the forensics delta untouched, got %d", a.lastCacheReadTokens)
		}
	})
}

// TestClear_ClearsRoundActivity verifies a conversation reset also drops the
// per-round clock and forensics delta: the new conversation starts with no
// provider contact, so the gates apply the first-turn presumption again.
func TestClear_ClearsRoundActivity(t *testing.T) {
	a := &Agent{}
	a.lastRoundActivity = time.Now()
	a.lastCacheReadTokens = 100
	a.Clear()
	if !a.lastRoundActivity.IsZero() {
		t.Fatalf("Clear must reset lastRoundActivity for the new conversation")
	}
	if a.lastCacheReadTokens != 0 {
		t.Fatalf("Clear must reset lastCacheReadTokens, got %d", a.lastCacheReadTokens)
	}
}
