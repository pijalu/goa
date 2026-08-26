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

// Hard limits are HARD limits (bugs.md 2026-08-26): at/above the hard ceiling
// no cache rationale may defer, skip, or soften the hard-layer strategy. These
// tests pin the cache-freedom of every hard path — the prefix-cache gate
// exists for the SOFT layer only.

// TestMaybeCompress_HardFiresWithHotCache pins the proactive hard branch: with
// usage at/above the configured hard ceiling and the provider cache presumed
// HOT (a turn just completed), maybeCompress must run the hard-layer strategy
// — never defer for cache warmth. Uses tool_elision as the hard strategy so
// the mutation is directly observable in history.
func TestMaybeCompress_HardFiresWithHotCache(t *testing.T) {
	long := strings.Repeat("x", 4000)
	makeHistory := func() []Message {
		return []Message{
			{Type: Content, Role: System, Content: "sys"},
			{Type: Content, Role: User, Content: "q1"},
			{Type: Content, Role: ToolRole, Content: long, ToolCallID: "c1"},
			{Type: Content, Role: ToolRole, Content: long, ToolCallID: "c2"},
			{Type: Content, Role: Assistant, Content: "a1"},
			{Type: Content, Role: User, Content: "q2"},
			{Type: Content, Role: Assistant, Content: "a2"},
		}
	}
	// Two 4000-char tool results ≈ 2000 est tokens; MaxTokens 2200 → usage ≈
	// 95% ≥ hard 80. The SOFT layer is left unset so only the hard branch can
	// act.
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  2200,
			Thresholds: CompressionThresholds{HardPercent: 80},
			Strategies: CompressionLayerStrategies{Hard: CompressionToolElision},
		},
	})
	a.history = makeHistory()
	a.lastTurnEnd = time.Now() // cache presumed hot — must NOT defer the hard tier

	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}
	if a.history[2].Content == long {
		t.Error("hard tier with a hot cache must still run (history unmutated)")
	}
}

// TestProactiveTierLocked_HardIgnoresCacheGate pins the tier selection: usage
// ≥ hard returns tierHard whether the cache is hot or cold. (The companion
// soft deferral is covered by TestMaybeCompress_ToolElision_DefersWhileCacheHot.)
func TestProactiveTierLocked_HardIgnoresCacheGate(t *testing.T) {
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  10000,
			Thresholds: CompressionThresholds{SoftPercent: 50, HardPercent: 90},
		},
	})
	rt := a.cfg.ContextCompression.resolveThresholds()

	a.mu.Lock()
	a.lastTurnEnd = time.Now() // hot
	hotTier := a.proactiveTierLocked(95, rt)
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour) // cold
	coldTier := a.proactiveTierLocked(95, rt)
	// Below hard but above soft: hot defers, cold runs — the gate applies to
	// the SOFT layer only.
	a.lastTurnEnd = time.Now()
	softHot := a.proactiveTierLocked(60, rt)
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour)
	softCold := a.proactiveTierLocked(60, rt)
	a.mu.Unlock()

	if hotTier != tierHard || coldTier != tierHard {
		t.Errorf("95%% usage: hot=%v cold=%v, want tierHard both", hotTier, coldTier)
	}
	if softHot != tierNone {
		t.Errorf("60%% usage with hot cache = %v, want tierNone (soft defers)", softHot)
	}
	if softCold != tierSoft {
		t.Errorf("60%% usage with cold cache = %v, want tierSoft", softCold)
	}
}

// TestMicroCompaction_OverHardRunsWithHotCache pins the overHard bypass in
// microCompactForced: even when micro is the configured maintenance strategy
// and the cache is hot, crossing the hard ceiling runs the mutation.
func TestMicroCompaction_OverHardRunsWithHotCache(t *testing.T) {
	long := strings.Repeat("x", 5000)
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  1700, // ≈95% full → over the default 95 hard? no: set hard 80 explicitly below
			Thresholds: CompressionThresholds{HardPercent: 80},
			Strategies: CompressionLayerStrategies{Soft: CompressionMicro},
			MicroCompaction: MicroCompactionConfig{
				KeepRecentMessages: 1,
				MinContentTokens:   10,
				CacheMissThreshold: time.Hour,
				TruncatedMarker:    "[cleared]",
				MinContextRatio:    0.5,
			},
		},
	})
	a.history = []Message{
		{Type: Content, Role: System, Content: "sys"},
		{Type: Content, Role: User, Content: "q1"},
		{Type: Content, Role: ToolRole, Content: long, ToolCallID: "c1"},
		{Type: Content, Role: Assistant, Content: "a1"},
		{Type: Content, Role: User, Content: "q2"},
		{Type: Content, Role: Assistant, Content: "a2"},
	}
	a.lastTurnEnd = time.Now() // hot cache — the overHard bypass must ignore it

	before, res := a.microCompactForced(false)
	_ = before
	if res.changed == 0 || a.history[2].Content != "[cleared]" {
		t.Errorf("micro at/above hard must mutate despite the hot cache; changed=%d content=%q",
			res.changed, a.history[2].Content[:min(40, len(a.history[2].Content))])
	}
}
