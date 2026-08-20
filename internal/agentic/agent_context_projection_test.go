// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// P20 / CX8 acceptance tests: token-meter projection. The projected figure
// anchors on the last provider-reported usage and reprices surface deltas
// (chars/token heuristic), so it reacts the moment a large tool result lands —
// before the next request — while the conservative estimate may still report
// the pre-delta surface.

func TestProjectedTokens_AnchorsOnProviderUsage(t *testing.T) {
	a := NewAgent(Config{Model: provider.Model{ContextWindow: 1000000}})
	a.SetHistory([]Message{
		{Type: Content, Role: User, Content: "hi"},
		{Type: Content, Role: Assistant, Content: "hello"},
	})
	recordTestUsage(a, &provider.Usage{InputTokens: 200, CacheReadTokens: 899800, OutputTokens: 100})

	stats := a.ContextStats()
	if stats.ProjectedTokens != 900000 {
		t.Errorf("ProjectedTokens = %d, want 900000 (provider gross input anchor)", stats.ProjectedTokens)
	}
	if stats.EstimatedTokens != 900000 {
		t.Errorf("EstimatedTokens = %d, want 900000", stats.EstimatedTokens)
	}
	if stats.UsagePercent != 90 {
		t.Errorf("UsagePercent = %d, want 90 (occupancy reads the projection)", stats.UsagePercent)
	}
}

// TestProjectedTokens_UpdatesAfterLargeToolResult is acceptance criterion 1:
// the projection must react immediately when a large tool result lands, before
// the next request reports usage. The estimate already does this via its floor,
// but the projection is the figure the footer and the compaction trigger read.
func TestProjectedTokens_UpdatesAfterLargeToolResult(t *testing.T) {
	a := NewAgent(Config{Model: provider.Model{ContextWindow: 1000000}})
	a.SetHistory([]Message{{Type: Content, Role: User, Content: "hi"}})
	recordTestUsage(a, &provider.Usage{InputTokens: 500000})

	// A large tool result arrives AFTER the usage line: the projection must
	// include its estimated cost immediately (before the next request).
	big := strings.Repeat("x", 100000) // ≈ 30k tokens at 3.3 chars/token
	toolMsg := Message{Type: Content, Role: ToolRole, Content: big, ToolCallID: "c1"}
	a.mu.Lock()
	a.history = append(a.history, toolMsg)
	a.mu.Unlock()

	stats := a.ContextStats()
	want := 500000 + messageTokenCount(&toolMsg)
	if stats.ProjectedTokens != want {
		t.Errorf("ProjectedTokens = %d, want %d (anchor + appended tool-result estimate)", stats.ProjectedTokens, want)
	}
}

// TestProjectedTokens_FallsBackToEstimateWithoutProviderUsage verifies that
// before any provider usage is recorded the projection equals the full
// heuristic estimate (no anchor yet).
func TestProjectedTokens_FallsBackToEstimateWithoutProviderUsage(t *testing.T) {
	a := NewAgent(Config{Model: provider.Model{ContextWindow: 1000000}})
	a.SetHistory([]Message{{Type: Content, Role: User, Content: "hi"}})
	stats := a.ContextStats()
	if stats.ProjectedTokens != stats.EstimatedTokens {
		t.Errorf("ProjectedTokens = %d, want == EstimatedTokens (%d) when no provider usage", stats.ProjectedTokens, stats.EstimatedTokens)
	}
}

// TestProjectedTokens_ClampedAtZero guards the dsh "clamped at zero" rule: a
// projected figure can never go negative (defensive; in practice history
// shrinkage invalidates the anchor, but the clamp keeps the invariant).
func TestProjectedTokens_ClampedAtZero(t *testing.T) {
	a := NewAgent(Config{Model: provider.Model{ContextWindow: 1000000}})
	a.SetHistory([]Message{{Type: Content, Role: User, Content: "hi"}})
	recordTestUsage(a, &provider.Usage{InputTokens: 500})
	a.mu.Lock()
	// Simulate a history mutation that did NOT invalidate (the projection
	// contract still clamps): replace history with nothing after the anchor.
	a.lastUsageHistoryLen = 0
	a.history = nil
	a.mu.Unlock()
	if got := a.ContextStats().ProjectedTokens; got < 0 {
		t.Errorf("ProjectedTokens = %d, want >= 0 (clamped)", got)
	}
}

// TestProjectedTokens_InvalidatedByHistoryShrink verifies the anchor is
// dropped on history-shrinking compaction, so the projection falls back to the
// small heuristic estimate rather than reporting the pre-compaction prompt.
func TestProjectedTokens_InvalidatedByHistoryShrink(t *testing.T) {
	a := NewAgent(Config{
		Model:              provider.Model{ContextWindow: 1000000},
		ContextCompression: ContextCompressionConfig{PreserveRecentTurns: 1},
	})
	history := []Message{{Type: Content, Role: System, Content: "sys"}}
	for i := 0; i < 6; i++ {
		history = append(history,
			Message{Type: Content, Role: User, Content: "question"},
			Message{Type: Content, Role: Assistant, Content: "answer"})
	}
	a.SetHistory(history)
	recordTestUsage(a, &provider.Usage{InputTokens: 900000})

	if got := a.ContextStats().ProjectedTokens; got != 900000 {
		t.Fatalf("pre-condition: ProjectedTokens = %d, want 900000", got)
	}

	a.mu.Lock()
	a.compressSelective()
	a.mu.Unlock()

	if got := a.ContextStats().ProjectedTokens; got >= 100000 {
		t.Errorf("ProjectedTokens = %d after selective, want the small chars-based value (stale anchor dropped)", got)
	}
}

// TestProactiveCompaction_SoftReadsProjection is acceptance criterion 3 under
// the soft/hard model: the proactive tier must read the projection, not the
// stale estimate. The setup makes the heuristic estimate sit ABOVE the soft
// ceiling while the provider-anchored projection sits BELOW it: only a tier
// reading the projection stays quiet. (This is the SOFT layer now — the old
// "trigger" layer was removed.)
func TestProactiveCompaction_SoftReadsProjection(t *testing.T) {
	a := NewAgent(Config{
		Model: provider.Model{ContextWindow: 100000},
		ContextCompression: ContextCompressionConfig{
			Thresholds: CompressionThresholds{SoftPercent: 50},
		},
	})
	// CJK-heavy history: the heuristic prices CJK at 1 token/char, so the full
	// surface estimate is ~60k tokens (60% of a 100k window) — ABOVE the 50%
	// trigger.
	a.SetHistory([]Message{
		{Type: Content, Role: User, Content: strings.Repeat("漢", 60000)},
	})
	// But the provider reports only 30k (30%) for that surface. The anchored
	// projection is 30k → 30%, BELOW the trigger.
	recordTestUsage(a, &provider.Usage{InputTokens: 30000})

	stats := a.ContextStats()
	if stats.EstimatedTokens < 50000 {
		t.Fatalf("setup: heuristic estimate (%d) must exceed the 50%% trigger (50000) to prove the trigger is not reading it", stats.EstimatedTokens)
	}
	if stats.ProjectedTokens >= 50000 {
		t.Fatalf("setup: projection (%d) must sit below the 50%% trigger (50000)", stats.ProjectedTokens)
	}
	if stats.UsagePercent >= 50 {
		t.Fatalf("setup: UsagePercent = %d, want < 50 (projection-based)", stats.UsagePercent)
	}

	rt := a.cfg.ContextCompression.resolveThresholds()
	tier := a.proactiveTier(rt, 100000)
	if tier != tierNone {
		t.Errorf("trigger read the stale estimate (tier=%v): must read the projection and stay quiet below the trigger", tier)
	}
}

// TestProactiveCompaction_SoftFiresAboveProjectedCeiling is the positive half
// of the projection-driven soft tier: when the projection crosses the soft
// ceiling, the soft tier fires. (Reads the projection, not the stale estimate.)
func TestProactiveCompaction_SoftFiresAboveProjectedCeiling(t *testing.T) {
	a := NewAgent(Config{
		Model: provider.Model{ContextWindow: 100000},
		ContextCompression: ContextCompressionConfig{
			Thresholds: CompressionThresholds{SoftPercent: 50},
		},
	})
	a.SetHistory([]Message{{Type: Content, Role: User, Content: "hi"}})
	recordTestUsage(a, &provider.Usage{InputTokens: 60000}) // 60% projection

	rt := a.cfg.ContextCompression.resolveThresholds()
	tier := a.proactiveTier(rt, 100000)
	if tier != tierSoft {
		t.Errorf("tier = %v, want tierSoft at 60%% projected usage", tier)
	}
}

// TestMaybeCompress_SoftReadsProjection runs the full maybeCompress path:
// with the projection below the soft ceiling but the stale estimate above it,
// no compression must fire (the per-round proactive gate reads the projection).
// Uses the SOFT layer (tool_elision) now that the trigger layer is removed.
func TestMaybeCompress_SoftReadsProjection(t *testing.T) {
	a := NewAgent(Config{
		Model: testModel(provider.ApiOpenAICompletions),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:  100000,
			Strategies: CompressionLayerStrategies{Soft: CompressionToolElision},
			Thresholds: CompressionThresholds{SoftPercent: 50},
		},
	})
	a.SetHistory([]Message{
		{Type: Content, Role: User, Content: strings.Repeat("漢", 60000)},
	})
	recordTestUsage(a, &provider.Usage{InputTokens: 30000})

	before := historyHash(a)
	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}
	if historyHash(a) != before {
		t.Errorf("maybeCompress mutated history below the projected trigger (must read the projection)")
	}
}
