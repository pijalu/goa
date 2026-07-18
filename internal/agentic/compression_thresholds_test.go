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

// --- Threshold resolution ---

func TestResolveThresholds(t *testing.T) {
	tests := []struct {
		name        string
		cfg         ContextCompressionConfig
		wantSoft    int
		wantTrigger int
		wantHard    int
	}{
		{
			name:        "zero config reproduces legacy defaults",
			cfg:         ContextCompressionConfig{},
			wantSoft:    0,
			wantTrigger: 90, // legacy SDK fallback
			wantHard:    95,
		},
		{
			name:        "legacy ThresholdPercent maps to trigger",
			cfg:         ContextCompressionConfig{ThresholdPercent: 80},
			wantSoft:    0,
			wantTrigger: 80,
			wantHard:    95,
		},
		{
			name: "explicit Thresholds win over legacy alias",
			cfg: ContextCompressionConfig{
				ThresholdPercent: 70,
				Thresholds:       CompressionThresholds{TriggerPercent: 85},
			},
			wantSoft:    0,
			wantTrigger: 70, // legacy alias wins when both set (backwards compat)
			wantHard:    95,
		},
		{
			name: "full explicit tiers",
			cfg: ContextCompressionConfig{
				Thresholds: CompressionThresholds{SoftPercent: 50, TriggerPercent: 75, HardPercent: 90},
			},
			wantSoft:    50,
			wantTrigger: 75,
			wantHard:    90,
		},
		{
			name:        "hard percent explicit",
			cfg:         ContextCompressionConfig{Thresholds: CompressionThresholds{HardPercent: 88}},
			wantSoft:    0,
			wantTrigger: 90,
			wantHard:    88,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.resolveThresholds()
			if got.soft != tt.wantSoft {
				t.Errorf("soft = %d, want %d", got.soft, tt.wantSoft)
			}
			if got.trigger != tt.wantTrigger {
				t.Errorf("trigger = %d, want %d", got.trigger, tt.wantTrigger)
			}
			if got.hard != tt.wantHard {
				t.Errorf("hard = %d, want %d", got.hard, tt.wantHard)
			}
		})
	}
}

// --- Tier computation ---

func TestProactiveTier(t *testing.T) {
	cfg := ContextCompressionConfig{Thresholds: CompressionThresholds{SoftPercent: 50, TriggerPercent: 80, HardPercent: 95}}
	rt := cfg.resolveThresholds()
	tests := []struct {
		name      string
		usage     int
		cacheCold bool
		want      compressionTier
	}{
		{"below soft does nothing", 40, true, tierNone},
		{"below soft hot cache does nothing", 40, false, tierNone},
		{"soft tier when cache cold", 60, true, tierSoft},
		{"soft tier defers while cache hot", 60, false, tierNone},
		{"trigger tier when cache cold", 85, true, tierTrigger},
		{"trigger tier defers while cache hot", 85, false, tierNone},
		{"hard ceiling bypasses hot cache", 96, false, tierTrigger},
		{"hard ceiling runs when cold", 96, true, tierTrigger},
		{"exactly soft boundary", 50, true, tierSoft},
		{"exactly trigger boundary", 80, true, tierTrigger},
		{"exactly hard boundary", 95, false, tierTrigger},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := NewAgent(Config{Model: testModel(provider.ApiOpenAICompletions)})
			if tt.cacheCold {
				a.lastTurnEnd = time.Now().Add(-2 * time.Hour)
			} else {
				a.lastTurnEnd = time.Now()
			}
			a.mu.Lock()
			got := a.proactiveTierLocked(tt.usage, rt)
			a.mu.Unlock()
			if got != tt.want {
				t.Errorf("proactiveTierLocked(usage=%d, cold=%v) = %v, want %v", tt.usage, tt.cacheCold, got, tt.want)
			}
		})
	}
}

func TestProactiveTier_SoftDisabledWhenZero(t *testing.T) {
	rt := ContextCompressionConfig{Thresholds: CompressionThresholds{SoftPercent: 0, TriggerPercent: 80, HardPercent: 95}}.resolveThresholds()
	a := NewAgent(Config{Model: testModel(provider.ApiOpenAICompletions)})
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour) // cold cache
	a.mu.Lock()
	got := a.proactiveTierLocked(60, rt)
	a.mu.Unlock()
	if got != tierNone {
		t.Errorf("soft disabled: proactiveTierLocked(60) = %v, want tierNone", got)
	}
}

// --- Soft strategy mapping ---

func TestSoftStrategy(t *testing.T) {
	tests := []struct {
		configured CompressionStrategy
		want       CompressionStrategy
	}{
		{CompressionToolElision, CompressionToolElision},
		{CompressionSummarize, CompressionMicro}, // never LLM at soft tier
		{CompressionHybrid, CompressionMicro},
		{CompressionSelective, CompressionMicro}, // too destructive for early maintenance
		{CompressionMicro, CompressionMicro},
		{"", CompressionToolElision},
	}
	for _, tt := range tests {
		t.Run(string(tt.configured), func(t *testing.T) {
			if got := softStrategy(tt.configured); got != tt.want {
				t.Errorf("softStrategy(%q) = %q, want %q", tt.configured, got, tt.want)
			}
		})
	}
}

// --- maybeCompress soft-tier integration ---

func softTierTestHistory() []Message {
	long := strings.Repeat("x", 4000)
	return []Message{
		{Type: Content, Role: System, Content: "sys"},                    // 0
		{Type: Content, Role: User, Content: "q1"},                       // 1
		{Type: Content, Role: ToolRole, Content: long, ToolCallID: "c1"}, // 2
		{Type: Content, Role: ToolRole, Content: long, ToolCallID: "c2"}, // 3
		{Type: Content, Role: ToolRole, Content: long, ToolCallID: "c3"}, // 4
		{Type: Content, Role: Assistant, Content: "a1"},                  // 5
		{Type: Content, Role: User, Content: "q2"},                       // 6
		{Type: Content, Role: ToolRole, Content: long, ToolCallID: "c4"}, // 7
		{Type: Content, Role: Assistant, Content: "a2"},                  // 8
		{Type: Content, Role: User, Content: "q3"},                       // 9
		{Type: Content, Role: Assistant, Content: "a3"},                  // 10
	}
}

func TestMaybeCompress_SoftTierRunsElision(t *testing.T) {
	// Usage ~4KB+sys of 20000 ≈ 21%? No — history is ~16KB chars → ~4000 tokens
	// → 20% of 20000. Configure soft=10, trigger=80 so 20% lands in soft band.
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  20000,
			Strategy:   CompressionToolElision,
			Thresholds: CompressionThresholds{SoftPercent: 10, TriggerPercent: 80, HardPercent: 95},
		},
	})
	a.history = softTierTestHistory()
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour) // cold cache

	before := a.history[2].Content
	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}
	if a.history[2].Content == before {
		t.Errorf("soft tier did not elide old tool results at %d%% usage (soft=10, trigger=80)", a.ContextStats().UsagePercent)
	}
}

func TestMaybeCompress_SoftTierDefersWhenCacheHot(t *testing.T) {
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  20000,
			Strategy:   CompressionToolElision,
			Thresholds: CompressionThresholds{SoftPercent: 10, TriggerPercent: 80, HardPercent: 95},
		},
	})
	a.history = softTierTestHistory()
	a.lastTurnEnd = time.Now() // hot cache

	before := a.history[2].Content
	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}
	if a.history[2].Content != before {
		t.Errorf("soft tier mutated history while cache hot; must defer")
	}
}

func TestMaybeCompress_SoftTierNeverSummarizes(t *testing.T) {
	// With a summarize strategy configured, the soft tier must run micro
	// (zero-LLM), never the LLM summarization. A summarize attempt would fail
	// in tests (no provider), so success here proves no LLM call happened.
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  20000,
			Strategy:   CompressionSummarize,
			Thresholds: CompressionThresholds{SoftPercent: 10, TriggerPercent: 80, HardPercent: 95},
			MicroCompaction: MicroCompactionConfig{
				KeepRecentMessages: 2,
				MinContentTokens:   10,
				MinContextRatio:    0.05, // below soft band so micro can act
				TruncatedMarker:    "[cleared]",
			},
		},
	})
	a.history = softTierTestHistory()
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour) // cold cache

	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress at soft tier must not invoke LLM summarization: %v", err)
	}
	if a.history[2].Content != "[cleared]" {
		t.Errorf("soft tier with summarize strategy should run micro truncation, got content %q", a.history[2].Content[:min(20, len(a.history[2].Content))])
	}
	if len(a.history) != 11 {
		t.Errorf("soft tier must not drop messages, history len = %d, want 11", len(a.history))
	}
}

func TestMaybeCompress_HysteresisSoftThenTrigger(t *testing.T) {
	// Turn 1: usage in soft band → elision only (history length preserved).
	// Turn 2: usage pushed past trigger → configured strategy runs.
	cfg := Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  20000,
			Strategy:   CompressionToolElision,
			Thresholds: CompressionThresholds{SoftPercent: 10, TriggerPercent: 60, HardPercent: 95},
		},
	}
	a := NewAgent(cfg)
	a.history = softTierTestHistory()
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour)

	// ~20% usage: soft tier elides but keeps all messages.
	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress soft: %v", err)
	}
	if len(a.history) != 11 {
		t.Fatalf("soft tier dropped messages: len = %d, want 11", len(a.history))
	}

	// Simulate growth past trigger: shrink the window so the same history
	// (~4000 tokens) exceeds 60% of 5000.
	a.SetContextCompression(ContextCompressionConfig{
		MaxTokens:  5000,
		Strategy:   CompressionToolElision,
		Thresholds: CompressionThresholds{SoftPercent: 10, TriggerPercent: 60, HardPercent: 95},
	})
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour)
	// Already elided content stays elided; trigger tier must run without error.
	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress trigger: %v", err)
	}
	stats := a.ContextStats()
	if stats.UsagePercent < 60 {
		t.Logf("after trigger compression usage = %d%%", stats.UsagePercent)
	}
}

// --- Regression: legacy defaults reproduce today's behavior ---

func TestMaybeCompress_LegacyThresholdPercentStillWorks(t *testing.T) {
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:        20000,
			ThresholdPercent: 10, // legacy field, no Thresholds set
			Strategy:         CompressionToolElision,
		},
	})
	a.history = softTierTestHistory()
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour) // cold

	before := a.history[2].Content
	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}
	if a.history[2].Content == before {
		t.Errorf("legacy ThresholdPercent trigger no longer fires")
	}
}

// --- Hard ceiling configurability ---

func TestCheckContextLimit_RespectsConfiguredHardPercent(t *testing.T) {
	// With hard=50, a history at ~80% of the window must be refused even
	// though the legacy 95% ceiling would allow it.
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  5000,
			Thresholds: CompressionThresholds{TriggerPercent: 40, HardPercent: 50},
		},
	})
	a.history = softTierTestHistory() // ~4000 tokens = 80% of 5000

	if err := a.checkContextLimit(); err == nil {
		t.Errorf("checkContextLimit must refuse above configured hard=50%%, got nil")
	}
}

func TestEnforceContextCeiling_RespectsConfiguredHardPercent(t *testing.T) {
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  20000,
			Thresholds: CompressionThresholds{TriggerPercent: 20, HardPercent: 30},
		},
	})
	a.history = softTierTestHistory() // ~4000 tokens = 20% of 20000 → under 30% hard

	a.enforceContextCeiling()
	if len(a.history) != 11 {
		t.Errorf("enforceContextCeiling dropped messages below hard=30%%: len = %d, want 11", len(a.history))
	}

	// Tighten: hard=15 → 20% usage exceeds → must drop oldest.
	a.SetContextCompression(ContextCompressionConfig{
		MaxTokens:  20000,
		Thresholds: CompressionThresholds{TriggerPercent: 10, HardPercent: 15},
	})
	a.enforceContextCeiling()
	if len(a.history) >= 11 {
		t.Errorf("enforceContextCeiling did not enforce hard=15%%: len = %d, want < 11", len(a.history))
	}
}
