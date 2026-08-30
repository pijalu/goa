// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// These tests pin the compression contracts after the opt-in rework:
//
//   - Every proactive layer is opt-in (0 = disabled). The shipped default
//     config (config/configs/default.yaml) sets hard_percent: 95 explicitly,
//     so the default UX is "hard tier ON at 95 with summarize" — but an
//     all-zero ContextCompressionConfig is fully disabled.
//   - With hard enabled (explicit 95), the actor at the hard tier is SUMMARIZE
//     (LLM); the destructive reactive message-drop ("ceiling") is only a
//     last-resort fallback, not the default actor.
//   - The only "hybrid" case is at the hard ceiling: if summarize cannot be
//     executed (its own request overflows the window), micro compaction is
//     applied as a pre-compression to make room — and ONLY then.
//   - ceiling / tool_elision / selective / micro must NOT fire proactively
//     under a zero config.
//
// TestResolveThresholds_DefaultHardStrategyIsSummarize pins the resolved
// defaults of an all-zero ContextCompressionConfig: the hard-layer strategy is
// summarize (not hybrid, not elision) whenever the hard tier is enabled, and
// soft/trigger stay opt-in off (zero config disables every tier — see
// TestProactiveTier_ZeroConfigAllTiersOff).
func TestResolveThresholds_DefaultHardStrategyIsSummarize(t *testing.T) {
	rt := ContextCompressionConfig{}.resolveThresholds()

	if rt.hardStrategy != CompressionSummarize {
		t.Errorf("default hardStrategy = %q, want %q", rt.hardStrategy, CompressionSummarize)
	}
	if rt.soft != 0 {
		t.Errorf("default soft must stay disabled (opt-in), got soft=%d", rt.soft)
	}
	if rt.softStrategy != CompressionMicro {
		t.Errorf("default softStrategy = %q, want %q", rt.softStrategy, CompressionMicro)
	}
}

// TestProactiveTier_HardFiresAt95 guards the opt-in contract: an explicit
// HardPercent 95 selects the hard tier at 95% usage — cache-hot or not
// (overflow risk beats cache churn) — while usage below it does nothing
// (soft/trigger still opt-in off). A negative HardPercent explicitly disables
// even the hard tier; zero config disables all tiers.
func TestProactiveTier_HardFiresAt95(t *testing.T) {
	rt := ContextCompressionConfig{Thresholds: CompressionThresholds{HardPercent: 95}}.resolveThresholds()
	cases := []struct {
		name      string
		usage     int
		cacheCold bool
		want      compressionTier
	}{
		{"low usage does nothing", 40, true, tierNone},
		{"low usage hot cache does nothing", 40, false, tierNone},
		{"94 hot cache still nothing", 94, false, tierNone},
		{"95 fires hard (boundary)", 95, true, tierHard},
		{"95 fires hard despite hot cache", 95, false, tierHard},
		{"96 fires hard despite hot cache", 96, false, tierHard},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			a := NewAgent(Config{Model: testModel(provider.ApiOpenAICompletions)})
			if tc.cacheCold {
				a.lastTurnEnd = time.Now().Add(-2 * time.Hour)
			} else {
				a.lastTurnEnd = time.Now()
			}
			a.mu.Lock()
			got := a.proactiveTierLocked(tc.usage, rt)
			a.mu.Unlock()
			if got != tc.want {
				t.Errorf("usage=%d cold=%v: tier = %v, want %v", tc.usage, tc.cacheCold, got, tc.want)
			}
		})
	}

	t.Run("negative hard disables the tier", func(t *testing.T) {
		rt := ContextCompressionConfig{Thresholds: CompressionThresholds{HardPercent: -1}}.resolveThresholds()
		a := NewAgent(Config{Model: testModel(provider.ApiOpenAICompletions)})
		a.lastTurnEnd = time.Now().Add(-2 * time.Hour)
		a.mu.Lock()
		got := a.proactiveTierLocked(99, rt)
		a.mu.Unlock()
		if got != tierNone {
			t.Errorf("hard=-1 must disable the hard tier, got %v", got)
		}
	})
}

// TestMaybeCompress_LegacyMicroBranchUsesEffectiveWindow pins the gate found
// broken in the live goa session (crash log "Context ceiling cannot be enforced
// ... 4750 tokens"): with strategy=micro (the default.yaml default) and a
// configured max_tokens far below the runtime model window, the legacy
// whole-strategy micro branch compared usage against the DISPLAY stats
// (denominator = runtime window, e.g. 204800) instead of the effective window.
// The branch then swallowed every turn below "95% of 204800" while the real
// usage against max_tokens sat at 120%+, masking the hard tier until the
// reactive ceiling fired and dropped messages. The gate must use the same
// effective window as the tier math.
func TestMaybeCompress_LegacyMicroBranchUsesEffectiveWindow(t *testing.T) {
	p := textEventProvider("Summary: the conversation was about testing.")
	a := NewAgent(Config{
		Model: testModel(p.API()),
		// Soft-layer micro (self-managing below the hard ceiling).
		ContextCompression: ContextCompressionConfig{
			Strategies: CompressionLayerStrategies{Soft: CompressionMicro},
			MaxTokens:  1000,
			Thresholds: CompressionThresholds{HardPercent: 95}, // shipped-default hard tier
			MicroCompaction: MicroCompactionConfig{
				Enabled: false, // as in default.yaml: micro self-management only
			},
		},
	})
	// Runtime-refreshed window dwarfs the configured cap, exactly like the
	// live session (model advertised 204800 while max_tokens was 5000).
	a.SetContextWindow(204800)

	a.history = []Message{
		{Type: Content, Role: System, Content: "sys"},
		{Type: Content, Role: User, Content: strings.Repeat("u", 2200)},
		{Type: Content, Role: Assistant, Content: strings.Repeat("a", 2200)},
	}
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}

	// The hard tier (default summarize at 95% of the 1000-token EFFECTIVE
	// window — usage here is ~134%) must act, not the micro branch.
	evs := compactEvents(obs)
	if len(evs) == 0 {
		t.Fatal("expected the hard tier to fire: usage ≥95% of the effective window")
	}
	for _, ev := range evs {
		if ev.Compaction == nil || ev.Compaction.Strategy != string(CompressionSummarize) {
			strat := "<nil>"
			if ev.Compaction != nil {
				strat = ev.Compaction.Strategy
			}
			t.Errorf("compaction strategy = %q, want %q (the legacy micro branch must not mask the hard tier via display stats)", strat, CompressionSummarize)
		}
	}
	if n := len(a.history); n != 2 {
		t.Errorf("after summarize the history must be the [summary-request, summary] pair, got %d messages", n)
	}
}

// TestMaybeCompress_LegacyMicroBranchStillSelfManagesBelowCeiling pins the
// other side of the same gate: below the effective hard ceiling the legacy
// micro branch keeps self-managing (microCompactForced applies its own ratio
// and cache gates), so the fix did not simply delete the branch.
func TestMaybeCompress_LegacyMicroBranchStillSelfManagesBelowCeiling(t *testing.T) {
	p := textEventProvider("Summary: never used.")
	a := NewAgent(Config{
		Model: testModel(p.API()),
		ContextCompression: ContextCompressionConfig{
			// Soft layer explicitly enabled (0 = disabled): the soft=micro
			// self-management branch only runs when the soft tier is on.
			Thresholds: CompressionThresholds{SoftPercent: 10},
			Strategies: CompressionLayerStrategies{Soft: CompressionMicro},
			MaxTokens:  1000,
			// Explicit micro settings (NewAgent only fills defaults for an
			// all-zero block): keep exactly the last message so the old tool
			// result at idx 2 falls inside the truncation window.
			MicroCompaction: MicroCompactionConfig{
				Enabled:            false,
				KeepRecentMessages: 1,
				MinContentTokens:   50,
				TruncatedMarker:    "[t]",
			},
		},
	})
	a.SetContextWindow(204800)

	// Cold cache (long idle) so micro's own gates admit the pass.
	a.mu.Lock()
	a.lastTurnEnd = time.Now().Add(-2 * time.Hour)
	a.history = []Message{
		{Type: Content, Role: System, Content: "sys"},
		{Type: Content, Role: User, Content: "ask"},
		{Type: Content, Role: ToolRole, Content: strings.Repeat("r", 3000)},
		{Type: Content, Role: Assistant, Content: "done"},
	}
	a.mu.Unlock()
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}

	// Usage against the effective window: ~(3000/3.3 + overhead) ≈ 55% < 95%,
	// so the branch must self-manage via micro (truncating the old tool
	// result), NOT run the hard-tier summarize.
	for _, ev := range compactEvents(obs) {
		if ev.Compaction == nil {
			continue
		}
		if ev.Compaction.Strategy == string(CompressionSummarize) {
			t.Errorf("summarize fired below the hard ceiling (usage ≈55%% of the effective window); the legacy micro branch should have handled the turn")
		}
	}
	// The tool result must have been truncated in place (marker), proving the
	// micro branch ran instead of doing nothing.
	a.mu.Lock()
	content := ""
	for i := range a.history {
		if a.history[i].Role == ToolRole {
			content = a.history[i].Content
		}
	}
	a.mu.Unlock()
	if content != "[t]" {
		t.Errorf("old tool result content = %q, want the micro truncation marker %q", content, "[t]")
	}
}

// TestMaybeCompress_DefaultHardCeilingRunsSummarizeNotCeiling is the headline
// regression: a default-config agent over the 95% window must compact via
// SUMMARIZE (LLM), leaving a valid [user→summary] history pair, and must NOT
// drop messages with the destructive ceiling enforcer. The export session
// showed "Context compacted (ceiling): 96% → 44% · 120 messages dropped" —
// with the default config the actor at 95% must be summarize.
func TestMaybeCompress_DefaultHardCeilingRunsSummarizeNotCeiling(t *testing.T) {
	p := textEventProvider("Summary: the conversation was about testing.")
	a := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "sys",
		Logger:       NewLogger(Error),
		// Shipped-default equivalent: hard tier explicitly enabled at 95.
		ContextCompression: ContextCompressionConfig{
			Thresholds: CompressionThresholds{HardPercent: 95},
		},
	})
	// Tiny window so the history is ≥95% full (chars/3.3 estimate: 4400 ascii
	// chars ≈ 1333 tokens + 3×4 message overhead ≈ 134% of 1000).
	a.cfg.Model.ContextWindow = 1000

	a.history = []Message{
		{Type: Content, Role: System, Content: "sys"},
		{Type: Content, Role: User, Content: strings.Repeat("u", 2200)},
		{Type: Content, Role: Assistant, Content: strings.Repeat("a", 2200)},
	}
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}

	evs := compactEvents(obs)
	if len(evs) == 0 {
		t.Fatal("expected an EventCompact from the hard layer at 95%+ usage")
	}
	for _, ev := range evs {
		if ev.Compaction == nil || ev.Compaction.Strategy != string(CompressionSummarize) {
			strat := "<nil>"
			if ev.Compaction != nil {
				strat = ev.Compaction.Strategy
			}
			t.Errorf("compaction strategy = %q, want %q (ceiling/elision must not be the default actor)", strat, CompressionSummarize)
		}
	}
	if n := len(a.history); n != 2 {
		t.Errorf("after summarize the history must be the [summary-request, summary] pair, got %d messages", n)
	}
	// And with the window now nearly empty, the reactive enforcer must no-op:
	a.enforceContextCeiling()
	for _, ev := range compactEvents(obs) {
		if ev.Compaction != nil && ev.Compaction.Strategy == "ceiling" {
			t.Error("ceiling enforcer fired after a successful summarize — it must be a last resort only")
		}
	}
}

// TestPreparePath_CeilingOnlyWhenSummarizeCannotRun pins the fallback order:
// when summarize itself cannot run (the summarization request fails with a
// non-overflow error — e.g. provider down), the last-resort ceiling enforcer
// still protects the window. This keeps the safety net while demoting it
// below summarize.
func TestPreparePath_CeilingOnlyWhenSummarizeCannotRun(t *testing.T) {
	// EveryRound: summarize retries transient failures through the provider
	// retry/backoff path (bugs.md summarize-429), so the fallback contract
	// needs a summarize that fails on EVERY attempt, not just the first.
	p := registerTestProviderEveryRound("summarize-hard-fail", []provider.AssistantMessageEvent{
		{Type: provider.EventError, Error: context.DeadlineExceeded},
	})
	a := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "sys",
		Logger:       NewLogger(Error),
		StreamOptions: provider.StreamOptions{
			MaxRetries: 1,
			RetryPolicy: &provider.RetryPolicy{
				Backoff: provider.RetryBackoff{
					InitialDelay: time.Millisecond,
					MaxDelay:     5 * time.Millisecond,
					Jitter:       0,
				},
			},
		},
	})
	// Tiny window; the history sits ≈134% so the default hard tier fires and
	// (when summarize fails) the ceiling enforcer has real work to do.
	a.cfg.Model.ContextWindow = 1000
	a.history = []Message{
		{Type: Content, Role: System, Content: "sys"},
		{Type: Content, Role: User, Content: strings.Repeat("u", 2200)},
		{Type: Content, Role: Assistant, Content: strings.Repeat("a", 2200)},
	}
	obs := &mockEventObserver{}
	a.AddObserver(obs)

	// maybeCompress reports the failure; the caller (prepareTurn) then lets
	// the ceiling enforcer protect the window — that ordering is the contract.
	_ = a.maybeCompress(context.Background())
	before := len(a.history)
	a.enforceContextCeiling()

	if len(a.history) >= before {
		t.Fatalf("ceiling enforcer must still drop messages when summarize cannot run; history %d -> %d", before, len(a.history))
	}
	found := false
	for _, ev := range compactEvents(obs) {
		if ev.Compaction != nil && ev.Compaction.Strategy == "hard fallback" {
			found = true
		}
	}
	if !found {
		t.Error("expected a 'hard fallback' compaction event when summarize cannot run (the old 'ceiling' label was removed)")
	}
}

// TestCompact_SummarizeOverflowAppliesMicroUnconditionally pins the "only
// hybrid case": at the hard ceiling, when summarize cannot be executed
// because its own request overflows the window, micro compaction is applied
// as a pre-compression to make room — and this fallback must NOT depend on
// micro_compaction.enabled being set (it is the summarize-overflow escape
// hatch, not an opt-in maintenance pass).
func TestCompact_SummarizeOverflowAppliesMicroUnconditionally(t *testing.T) {
	// First summarize call overflows (context-length error), retry succeeds.
	p := registerOverflowProvider("summarize-overflow-micro-off", 1)
	a := NewAgent(Config{
		Model:        testModel(p.API()),
		SystemPrompt: "sys",
		Logger:       NewLogger(Error),
		// Micro NOT enabled (zero value): the overflow fallback must still apply.
	})
	a.history = microToolHistory(2000, 6)

	obs := &mockEventObserver{}
	a.AddObserver(obs)

	if err := a.Compact(context.Background()); err != nil {
		t.Fatalf("Compact: %v", err)
	}
	p.mu.Lock()
	attempts := len(p.received)
	summaries := p.summaries
	p.mu.Unlock()
	if attempts != 2 {
		t.Fatalf("summarize attempts = %d, want 2 (overflowed summarize + retry after micro)", attempts)
	}
	if summaries != 1 {
		t.Fatalf("successful summarizes = %d, want 1", summaries)
	}
	// The micro fallback must have fired between the two summarize attempts.
	sawMicroFallback := false
	for _, ev := range compactEvents(obs) {
		if ev.Compaction != nil && ev.Compaction.Strategy == string(CompressionMicro) && ev.Compaction.Detail == "summarize overflow fallback" {
			sawMicroFallback = true
		}
	}
	if !sawMicroFallback {
		t.Error("micro compaction was never applied although summarize overflowed and micro_compaction.enabled is false")
	}
	if n := len(a.history); n != 2 {
		t.Errorf("post-Compact history = %d messages, want the [summary-request, summary] pair (2)", n)
	}
}
