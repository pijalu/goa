// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"testing"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestForegroundOrchestrator_CacheStatsCallback proves the per-agent observer
// relays a sub-agent's final EventTokenStats — role, bound goal ID, and cache
// usage — to the installed callback (bugs.md: /stats:cache must section per
// agent/goal, so companion/stage agent cache stats must reach the session
// turn recorder).
func TestForegroundOrchestrator_CacheStatsCallback(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	type got struct{ role, goalID string }
	var calls []got
	var usage SubAgentCacheUsage
	orch.SetCacheStatsCallback(func(role, goalID string, u SubAgentCacheUsage) {
		calls = append(calls, got{role, goalID})
		usage = u
	}, func() string { return "goal-9" })

	// Create a pool agent (wires the per-agent observer via OnAgentCreated)
	// and feed a token-stats event through forwardCacheStats directly — the
	// same path the observer invokes on EventTokenStats.
	agent, err := pool.GetOrCreate("companion")
	if err != nil {
		t.Fatalf("GetOrCreate: %v", err)
	}
	if agent == nil {
		t.Fatal("companion agent is nil")
	}

	ev := agentic.OutputEvent{
		Type:    agentic.EventTokenStats,
		Timings: &agentic.TokenTimings{PromptN: 1000, PredictedN: 50, CacheReadTokens: 900, CacheWriteTokens: 100},
	}
	orch.forwardCacheStats("companion", ev)

	if len(calls) != 1 {
		t.Fatalf("callback calls = %d, want 1", len(calls))
	}
	if calls[0].role != "companion" || calls[0].goalID != "goal-9" {
		t.Errorf("callback identity = %+v, want companion/goal-9", calls[0])
	}
	if usage.CacheRead != 900 || usage.CacheWrite != 100 || usage.PromptN != 1000 {
		t.Errorf("callback usage = %+v, want read 900 write 100 prompt 1000", usage)
	}
}

// TestForegroundOrchestrator_CacheStatsNilCallback proves non-token events and
// a nil callback are safe no-ops.
func TestForegroundOrchestrator_CacheStatsNilCallback(t *testing.T) {
	pool := NewAgentPool(testModel("test-model"), provider.StreamOptions{}, nil)
	orch := NewForegroundOrchestrator(pool)

	// No callback installed: must not panic.
	orch.forwardCacheStats("companion", agentic.OutputEvent{
		Type:    agentic.EventTokenStats,
		Timings: &agentic.TokenTimings{PromptN: 1},
	})
	// Non-token event with a callback installed: must not fire.
	var fired bool
	orch.SetCacheStatsCallback(func(string, string, SubAgentCacheUsage) { fired = true }, nil)
	orch.forwardCacheStats("companion", agentic.OutputEvent{Type: agentic.EventContent})
	orch.forwardCacheStats("companion", agentic.OutputEvent{Type: agentic.EventTokenStats}) // nil Timings
	if fired {
		t.Error("callback fired for a non-token/nil-timings event")
	}
}
