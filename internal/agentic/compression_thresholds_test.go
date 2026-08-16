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

// --- Threshold resolution (soft / hard / on-error model) ---

func TestResolveThresholds(t *testing.T) {
	tests := []struct {
		name     string
		cfg      ContextCompressionConfig
		wantSoft int
		wantHard int
	}{
		{
			name: "zero config: both layers off (0 = disabled)",
			cfg:  ContextCompressionConfig{},
			// soft/hard are opt-in (0 = disabled); the shipped default config
			// sets hard 95 explicitly — a zero SDK config is fully off.
			wantSoft: 0,
			wantHard: 0,
		},
		{
			name:     "full explicit tiers",
			cfg:      ContextCompressionConfig{Thresholds: CompressionThresholds{SoftPercent: 50, HardPercent: 90}},
			wantSoft: 50,
			wantHard: 90,
		},
		{
			name:     "hard percent explicit",
			cfg:      ContextCompressionConfig{Thresholds: CompressionThresholds{HardPercent: 88}},
			wantSoft: 0,
			wantHard: 88,
		},
		{
			name: "negative soft clamps to disabled; negative hard stays (legacy disable spelling)",
			cfg:  ContextCompressionConfig{Thresholds: CompressionThresholds{SoftPercent: -1, HardPercent: -1}},
			// soft: negative = disabled (0). hard: negative is the legacy
			// explicit-disable spelling — hardEnabled() treats <=0 as disabled;
			// effectiveHard() still falls back to 95 for the reactive math only.
			wantSoft: 0,
			wantHard: -1,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.cfg.resolveThresholds()
			if got.soft != tt.wantSoft {
				t.Errorf("soft = %d, want %d", got.soft, tt.wantSoft)
			}
			if got.hard != tt.wantHard {
				t.Errorf("hard = %d, want %d", got.hard, tt.wantHard)
			}
		})
	}
}

// --- Tier computation (soft/hard only) ---

func TestProactiveTier(t *testing.T) {
	cfg := ContextCompressionConfig{Thresholds: CompressionThresholds{SoftPercent: 50, HardPercent: 95}}
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
		{"exactly soft boundary", 50, true, tierSoft},
		// The hard tier ALWAYS fires at/above the hard ceiling, cache state
		// notwithstanding (the cache gate only defers SOFT maintenance).
		{"hard ceiling bypasses hot cache", 96, false, tierHard},
		{"hard ceiling runs when cold", 96, true, tierHard},
		{"exactly hard boundary", 95, false, tierHard},
		{"exactly hard boundary cold", 95, true, tierHard},
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

// TestProactiveTier_ZeroConfigAllTiersOff guards the opt-in contract: with
// no thresholds configured (all 0), NO proactive tier fires at any usage —
// soft and hard are both opt-in (0 = disabled).
func TestProactiveTier_ZeroConfigAllTiersOff(t *testing.T) {
	rt := ContextCompressionConfig{}.resolveThresholds()
	a := NewAgent(Config{Model: testModel(provider.ApiOpenAICompletions)})
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour) // cold cache: would fire if enabled
	for _, usage := range []int{50, 80, 90, 94, 95, 99} {
		a.mu.Lock()
		got := a.proactiveTierLocked(usage, rt)
		a.mu.Unlock()
		if got != tierNone {
			t.Errorf("proactiveTierLocked(%d%%, zero config) = %v, want tierNone (all tiers opt-in off)", usage, got)
		}
	}
}

// TestMaybeCompress_ZeroThresholdsNoProactive confirms maybeCompress is a no-op
// by default: it neither mutates nor drops history when every proactive
// threshold is disabled.
func TestMaybeCompress_ZeroThresholdsNoProactive(t *testing.T) {
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  20000,
			Strategies: CompressionLayerStrategies{Soft: CompressionToolElision}, // strategy set, no thresholds
		},
	})
	a.history = softTierTestHistory()
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour) // cold cache

	before := a.history[2].Content
	beforeLen := len(a.history)
	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}
	if a.history[2].Content != before || len(a.history) != beforeLen {
		t.Errorf("maybeCompress with zero thresholds must be a no-op (no proactive compression)")
	}
}

// TestMaybeCompress_ZeroConfigMicroSoftStrategyIsNoOp pins the regression
// behind the spurious "Context compacted (micro): 0% → 1%" banner: with a
// zero compression config the soft layer is DISABLED, but resolveThresholds
// still defaults softStrategy to micro. The soft=micro self-management branch
// in maybeCompress keyed off the resolved strategy alone and fired micro on
// every turn below the ceiling — truncating fresh tool results at near-empty
// context. The gate must require the soft threshold (rt.soft > 0).
func TestMaybeCompress_ZeroConfigMicroSoftStrategyIsNoOp(t *testing.T) {
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens: 20000,
			// No Strategies and no Thresholds: soft resolves to micro (default)
			// but SoftPercent is 0 = disabled. No layer may fire.
		},
	})
	a.history = []Message{
		{Type: Content, Role: System, Content: "sys"},
		{Type: Content, Role: User, Content: "ask"},
		{Type: Content, Role: ToolRole, Content: strings.Repeat("r", 3000)},
		{Type: Content, Role: Assistant, Content: "done"},
	}
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour) // cold cache: would fire if enabled

	// Snapshot the full history (content + length) to detect any in-place
	// micro truncation or message drop.
	type msg struct {
		role    Role
		content string
	}
	snap := func() []msg {
		out := make([]msg, len(a.history))
		for i, m := range a.history {
			out[i] = msg{m.Role, m.Content}
		}
		return out
	}
	before := snap()

	obs := &mockEventObserver{}
	a.AddObserver(obs)

	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}

	after := snap()
	if len(after) != len(before) {
		t.Fatalf("maybeCompress changed history length %d → %d with all layers disabled", len(before), len(after))
	}
	for i := range before {
		if after[i] != before[i] {
			t.Errorf("maybeCompress mutated message %d with all layers disabled: content %d → %d bytes", i, len(before[i].content), len(after[i].content))
		}
	}
	for _, ev := range compactEvents(obs) {
		if ev.Compaction != nil {
			t.Errorf("compaction event emitted with all layers disabled: %+v", ev.Compaction)
		}
	}
}

func TestProactiveTier_SoftDisabledWhenNegative(t *testing.T) {
	// SoftPercent -1 disables the soft layer entirely: usage in the soft band
	// does nothing even with a cold cache.
	rt := ContextCompressionConfig{Thresholds: CompressionThresholds{SoftPercent: -1, HardPercent: 95}}.resolveThresholds()
	a := NewAgent(Config{Model: testModel(provider.ApiOpenAICompletions)})
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour) // cold cache
	a.mu.Lock()
	got := a.proactiveTierLocked(60, rt)
	a.mu.Unlock()
	if got != tierNone {
		t.Errorf("soft disabled: proactiveTierLocked(60) = %v, want tierNone", got)
	}
}

// --- Per-layer strategy resolution (soft/hard) ---

func TestResolveThresholdsStrategies(t *testing.T) {
	tests := []struct {
		name        string
		cfg         ContextCompressionConfig
		wantSoft    CompressionStrategy
		wantHard    CompressionStrategy
		wantSoftPct int // expected resolved soft percent (0 = disabled; opt-in)
	}{
		{
			name:        "defaults: micro soft, summarize hard, both levels off",
			cfg:         ContextCompressionConfig{},
			wantSoft:    CompressionMicro,
			wantHard:    CompressionSummarize,
			wantSoftPct: 0,
		},
		{
			name: "explicit per-layer strategies win",
			cfg: ContextCompressionConfig{
				Strategies: CompressionLayerStrategies{Soft: CompressionToolElision, Hard: CompressionSelective},
			},
			wantSoft:    CompressionToolElision,
			wantHard:    CompressionSelective,
			wantSoftPct: 0,
		},
		{
			name: "soft layer honors any configured strategy",
			cfg: ContextCompressionConfig{
				Strategies: CompressionLayerStrategies{Soft: CompressionSummarize},
			},
			wantSoft:    CompressionSummarize,
			wantHard:    CompressionSummarize,
			wantSoftPct: 0,
		},
		{
			name:        "negative soft percent clamps to disabled",
			cfg:         ContextCompressionConfig{Thresholds: CompressionThresholds{SoftPercent: -1}},
			wantSoft:    CompressionMicro,
			wantHard:    CompressionSummarize,
			wantSoftPct: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rt := tt.cfg.resolveThresholds()
			if rt.softStrategy != tt.wantSoft {
				t.Errorf("softStrategy = %q, want %q", rt.softStrategy, tt.wantSoft)
			}
			if rt.hardStrategy != tt.wantHard {
				t.Errorf("hardStrategy = %q, want %q", rt.hardStrategy, tt.wantHard)
			}
			if rt.soft != tt.wantSoftPct {
				t.Errorf("soft percent = %d, want %d", rt.soft, tt.wantSoftPct)
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
	// History is ~16KB chars → ~4000 tokens → 20% of 20000. soft=10 → soft band.
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  20000,
			Strategies: CompressionLayerStrategies{Soft: CompressionToolElision},
			Thresholds: CompressionThresholds{SoftPercent: 10, HardPercent: 95},
		},
	})
	a.history = softTierTestHistory()
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour) // cold cache

	before := a.history[2].Content
	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}
	if a.history[2].Content == before {
		t.Errorf("soft tier did not elide old tool results at %d%% usage (soft=10)", a.ContextStats().UsagePercent)
	}
}

func TestMaybeCompress_SoftTierDefersWhenCacheHot(t *testing.T) {
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  20000,
			Strategies: CompressionLayerStrategies{Soft: CompressionToolElision},
			Thresholds: CompressionThresholds{SoftPercent: 10, HardPercent: 95},
		},
	})
	a.history = softTierTestHistory()
	a.lastTurnEnd = time.Now() // hot cache

	before := a.history[2].Content
	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}
	if a.history[2].Content != before {
		t.Errorf("soft tier mutated history while cache hot; must defer (hard tier only bypasses the gate)")
	}
}

// TestMaybeCompress_SoftTierHonorsSummarize pins the all-methods soft layer:
// an explicit summarize strategy at the soft tier must run the LLM
// summarization (no degradation to micro). The history becomes the
// [summary-request, summary] pair.
func TestMaybeCompress_SoftTierHonorsSummarize(t *testing.T) {
	p := textEventProvider("Summary: soft tier summary.")
	a := NewAgent(Config{
		Model: testModel(p.API()),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  20000,
			Strategies: CompressionLayerStrategies{Soft: CompressionSummarize},
			Thresholds: CompressionThresholds{SoftPercent: 10, HardPercent: 95},
		},
	})
	a.history = softTierTestHistory()
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour) // cold cache

	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress at soft tier with summarize: %v", err)
	}
	if n := len(a.history); n != 2 {
		t.Errorf("soft tier with summarize must compact to the [summary-request, summary] pair, got %d messages", n)
	}
}

func TestMaybeCompress_HysteresisSoftThenHard(t *testing.T) {
	// Turn 1: usage in soft band → elision only (history length preserved).
	// Turn 2: usage pushed past the hard ceiling → hard strategy runs.
	cfg := Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  20000,
			Strategies: CompressionLayerStrategies{Soft: CompressionToolElision, Hard: CompressionSelective},
			Thresholds: CompressionThresholds{SoftPercent: 10, HardPercent: 95},
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

	// Simulate growth past the hard ceiling: shrink the window so the same
	// history (~4000 tokens) exceeds 95% of 4000.
	a.SetContextCompression(ContextCompressionConfig{
		MaxTokens:  4000,
		Strategies: CompressionLayerStrategies{Soft: CompressionToolElision, Hard: CompressionSelective},
		Thresholds: CompressionThresholds{SoftPercent: 10, HardPercent: 95},
	})
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour)
	// Hard tier must run (selective, offline) without error.
	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress hard: %v", err)
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
			Thresholds: CompressionThresholds{HardPercent: 50},
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
			Thresholds: CompressionThresholds{HardPercent: 30},
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
		Thresholds: CompressionThresholds{HardPercent: 15},
	})
	a.enforceContextCeiling()
	if len(a.history) >= 11 {
		t.Errorf("enforceContextCeiling did not enforce hard=15%%: len = %d, want < 11", len(a.history))
	}
}

// TestProactiveTier_DisableCacheGate: with the cache gate off, a hot cache
// never defers SOFT maintenance — it fires immediately at the soft level.
func TestProactiveTier_DisableCacheGate(t *testing.T) {
	rt := ContextCompressionConfig{Thresholds: CompressionThresholds{SoftPercent: 80, HardPercent: 95}}.resolveThresholds()
	a := NewAgent(Config{
		Model:              testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{DisableCacheGate: true},
	})
	a.lastTurnEnd = time.Now() // hot cache — would normally defer soft
	a.mu.Lock()
	got := a.proactiveTierLocked(82, rt)
	a.mu.Unlock()
	if got != tierSoft {
		t.Errorf("cache gate off: proactiveTierLocked(82, hot) = %v, want tierSoft (no deferral)", got)
	}
	// Sanity: same setup with the gate on defers.
	b := NewAgent(Config{Model: testModel(provider.ApiOpenAICompletions)})
	b.lastTurnEnd = time.Now()
	b.mu.Lock()
	gotOn := b.proactiveTierLocked(82, rt)
	b.mu.Unlock()
	if gotOn != tierNone {
		t.Errorf("cache gate on: proactiveTierLocked(82, hot) = %v, want tierNone (soft deferred)", gotOn)
	}
}
