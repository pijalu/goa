// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// Proactive (threshold-triggered) tool_elision mutates old messages in place,
// which churns the provider's hot prefix cache into a full re-process on the
// next turn — exactly the cost the cache-aware micro-compaction gate exists to
// avoid. The default elision strategy must apply the SAME cache-cold deferral:
// while the cache is presumed hot (recent turn), proactive elision defers; once
// cold (long idle gap), it applies. Regression test for B5.
func TestMaybeCompress_ToolElision_DefersWhileCacheHot(t *testing.T) {
	makeHistory := func() []Message {
		// Long tool results old enough to be elided (beyond the preserve window).
		long := strings.Repeat("x", 4000)
		return []Message{
			{Type: Content, Role: System, Content: "sys"}, // 0
			{Type: Content, Role: User, Content: "q1"}, // 1
			{Type: Content, Role: ToolRole, Content: long, ToolCallID: "c1"}, // 2 <- elided (idx < boundary)
			{Type: Content, Role: ToolRole, Content: long, ToolCallID: "c2"}, // 3 <- elided
			{Type: Content, Role: ToolRole, Content: long, ToolCallID: "c3"}, // 4 <- elided
			{Type: Content, Role: Assistant, Content: "a1"}, // 5
			{Type: Content, Role: User, Content: "q2"}, // 6
			{Type: Content, Role: ToolRole, Content: long, ToolCallID: "c4"}, // 7
			{Type: Content, Role: Assistant, Content: "a2"}, // 8
			{Type: Content, Role: User, Content: "q3"}, // 9
			{Type: Content, Role: Assistant, Content: "a3"}, // 10
		}
		// boundary = 11 - preserve(2)*3 = 5 → indices 1..4 elided; 2,3,4 are tool results.
	}

	newCfg := func() Config {
		return Config{
			Model: testModel(provider.ApiOpenAICompletions),
			ContextCompression: ContextCompressionConfig{
				// Large enough ceiling that the ~4KB history sits between the
				// proactive threshold (10%) and the 95% hard-ceiling override,
				// so the cache gate — not the emergency override — decides.
				MaxTokens:        20000,
				ThresholdPercent: 10,
				Strategy:         CompressionToolElision,
			},
		}
	}

	tests := []struct {
		name string
		// setup tweaks the agent's cache state after history is loaded.
		setup func(a *Agent)
		// wantMutation reports whether proactive elision should have rewritten
		// the old tool result at history[2].
		wantMutation bool
	}{
		{
			name: "hot cache defers proactive elision",
			// Simulate a turn that JUST finished (cache presumed hot).
			setup:        func(a *Agent) { a.lastTurnEnd = time.Now() },
			wantMutation: false,
		},
		{
			name: "cold cache applies proactive elision",
			// Idle far longer than any cache TTL → cache presumed cold.
			setup:        func(a *Agent) { a.lastTurnEnd = time.Now().Add(-2 * time.Hour) },
			wantMutation: true,
		},
		{
			// First turn (zero lastTurnEnd): the cold presumption must expire
			// once a completed request reports cache_read > 0 — otherwise the
			// gate fails open for the entire first turn and churns a
			// demonstrably hot cache (bugs.md "Micro-compaction cache gate
			// fails open during the entire first turn").
			name: "first turn with warm observation defers proactive elision",
			// lastTurnEnd zero: still in the session's first turn, but round
			// 1's completed request reported cache hits.
			setup:        func(a *Agent) { a.cacheWarmObserved = true },
			wantMutation: false,
		},
		{
			// lastTurnEnd zero and no cache hits reported: genuine cold cache.
			name:         "first turn without warm observation applies proactive elision",
			setup:        func(a *Agent) {},
			wantMutation: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAgent(newCfg())
			a.history = makeHistory()
			tt.setup(a)

			before := a.history[2].Content
			if err := a.maybeCompress(context.Background()); err != nil {
				t.Fatalf("maybeCompress: %v", err)
			}
			if mutated := a.history[2].Content != before; mutated != tt.wantMutation {
				t.Errorf("proactive elision mutation = %v, want %v", mutated, tt.wantMutation)
			}
		})
	}
}

// The cache-hot deferral must stop protecting the cache near the window:
// above the deferral ceiling (hard−10 = 85% with defaults), overflow risk
// beats cache churn. Regression test for the deepseek-v4 incident, where a
// permanently-hot cache (99.7% hit) suppressed every proactive compression
// from the 50% trigger all the way to a provider-side rejection at 100%.
func TestMicroCompaction_DeferralCeilingOverridesHotCache(t *testing.T) {
	long := strings.Repeat("x", 5000) // ≈ 1519 est tokens incl. overhead
	makeHistory := func() []Message {
		return []Message{
			{Type: Content, Role: System, Content: "sys"},
			{Type: Content, Role: User, Content: "q1"},
			{Type: Content, Role: ToolRole, Content: long, ToolCallID: "c1"},
			{Type: Content, Role: Assistant, Content: "a1"},
			{Type: Content, Role: User, Content: "q2"},
			{Type: Content, Role: Assistant, Content: "a2"},
		}
	}
	newMicroCfg := func(maxTokens int) Config {
		return Config{
			Model: testModel(provider.ApiOpenAICompletions),
			ContextCompression: ContextCompressionConfig{
				MaxTokens: maxTokens,
				Strategy:  CompressionMicro,
				MicroCompaction: MicroCompactionConfig{
					KeepRecentMessages: 1,
					MinContentTokens:   10,
					CacheMissThreshold: time.Hour,
					TruncatedMarker:    "[cleared]",
					MinContextRatio:    0.5,
				},
			},
		}
	}

	t.Run("above ceiling applies despite hot cache", func(t *testing.T) {
		// ratio ≈ 1539/1700 = 90% — above the 85% deferral ceiling, below the
		// 95% hard ceiling, with a turn that JUST finished (cache hot).
		a := NewAgent(newMicroCfg(1700))
		a.history = makeHistory()
		a.lastTurnEnd = time.Now()

		if err := a.maybeCompress(context.Background()); err != nil {
			t.Fatalf("maybeCompress: %v", err)
		}
		if a.history[2].Content != "[cleared]" {
			t.Errorf("micro compaction must apply above the deferral ceiling even with a hot cache; content=%q",
				a.history[2].Content[:min(40, len(a.history[2].Content))])
		}
	})

	t.Run("below ceiling still defers with hot cache", func(t *testing.T) {
		// ratio ≈ 1539/1900 = 81% — below the 85% deferral ceiling.
		a := NewAgent(newMicroCfg(1900))
		a.history = makeHistory()
		a.lastTurnEnd = time.Now()

		if err := a.maybeCompress(context.Background()); err != nil {
			t.Fatalf("maybeCompress: %v", err)
		}
		if a.history[2].Content != long {
			t.Errorf("micro compaction must keep deferring below the deferral ceiling when the cache is hot")
		}
	})
}
