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

	t.Run("hot cache defers proactive elision", func(t *testing.T) {
		a := NewAgent(newCfg())
		a.history = makeHistory()
		// Simulate a turn that JUST finished (cache presumed hot).
		a.lastTurnEnd = time.Now()

		before := a.history[2].Content
		if err := a.maybeCompress(context.Background()); err != nil {
			t.Fatalf("maybeCompress: %v", err)
		}
		if a.history[2].Content != before {
			t.Errorf("proactive elision mutated history while the cache was hot; " +
				"it should defer to avoid churning the provider prefix cache")
		}
	})

	t.Run("cold cache applies proactive elision", func(t *testing.T) {
		a := NewAgent(newCfg())
		a.history = makeHistory()
		// Idle far longer than any cache TTL → cache presumed cold.
		a.lastTurnEnd = time.Now().Add(-2 * time.Hour)

		before := a.history[2].Content
		if err := a.maybeCompress(context.Background()); err != nil {
			t.Fatalf("maybeCompress: %v", err)
		}
		if a.history[2].Content == before {
			t.Errorf("proactive elision did NOT run on a cold cache; " +
				"old tool results should have been elided")
		}
	})
}
