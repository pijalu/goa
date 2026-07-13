// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"strings"
	"testing"
)

// TestFixedCostTokens_IncludesSystemAndTools verifies the per-turn fixed cost
// accounts for both the system prompt and the serialized tool schemas (not just
// history). Previously these were excluded, so usage was systematically
// underestimated and the proactive threshold fired a turn too late.
func TestFixedCostTokens_IncludesSystemAndTools(t *testing.T) {
	sysPrompt := strings.Repeat("system line. ", 40) // ~200 tokens
	a := NewAgent(Config{
		SystemPrompt: sysPrompt,
		Tools: []Tool{
			&fakeTool{name: "alpha"},
			&fakeTool{name: "beta_tool"},
		},
		Logger: NewLogger(Error),
	})

	fixed := a.fixedCostTokens()
	systemOnly := estimateTokens(sysPrompt)
	if fixed <= systemOnly {
		t.Fatalf("fixed cost must include tool schemas: fixed=%d, system-only=%d", fixed, systemOnly)
	}
	if fixed <= 0 {
		t.Fatalf("fixed cost must be positive, got %d", fixed)
	}

	// Stats must reflect history + fixed cost.
	a.mu.Lock()
	a.history = []Message{{Type: Content, Role: User, Content: strings.Repeat("x", 400)}}
	a.mu.Unlock()
	histTokens := estimateTokensFromHistory(a.history)

	stats := a.ContextStats()
	if stats.EstimatedTokens != histTokens+fixed {
		t.Fatalf("EstimatedTokens must include fixed cost: got %d, want %d (history %d + fixed %d)",
			stats.EstimatedTokens, histTokens+fixed, histTokens, fixed)
	}
}

// TestMaybeCompress_FiresDueToFixedCost verifies the proactive threshold now
// accounts for fixed cost: a history whose tokens alone are under the threshold
// but whose tokens + fixed cost cross it must still trigger compression.
// Previously the fixed cost was ignored, compaction was skipped, and a large
// next-turn tool result blew past 100% before the reactive path caught it.
func TestMaybeCompress_FiresDueToFixedCost(t *testing.T) {
	// Large system prompt dominates the fixed cost so the threshold crossing
	// is unambiguous.
	sysPrompt := strings.Repeat("guideline. ", 400) // ~200 tokens
	a := NewAgent(Config{
		SystemPrompt: sysPrompt,
		Tools: []Tool{
			&fakeTool{name: "reader"},
			&fakeTool{name: "writer"},
			&fakeTool{name: "searcher"},
		},
		Logger: NewLogger(Error),
		ContextCompression: ContextCompressionConfig{
			MaxTokens:           1000,
			Strategy:            CompressionSelective,
			ThresholdPercent:    90,
			PreserveRecentTurns: 1,
		},
	})

	// History tokens well under 90% of 1000 on its own...
	hist := historyWithNToolResults(6, 120) // ~6 * 30 = ~180 tokens << 900
	a.mu.Lock()
	a.history = hist
	nBefore := len(a.history)
	a.mu.Unlock()

	histOnly := estimateTokensFromHistory(hist)
	if histOnly >= 900 {
		t.Fatalf("test setup: history-only tokens %d should be under the 900 threshold", histOnly)
	}
	// ...but with fixed cost the full request crosses 90%.
	if histOnly+a.fixedCostTokens() < 900 {
		t.Fatalf("test setup: history %d + fixed %d should cross the 900 threshold", histOnly, a.fixedCostTokens())
	}

	if err := a.maybeCompress(context.Background()); err != nil {
		t.Fatalf("maybeCompress: %v", err)
	}

	a.mu.Lock()
	nAfter := len(a.history)
	a.mu.Unlock()
	if nAfter >= nBefore {
		t.Fatalf("proactive compaction did not fire despite fixed-cost overflow: %d -> %d messages", nBefore, nAfter)
	}
}
