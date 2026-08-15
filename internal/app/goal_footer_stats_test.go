// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

// TestGoalTurn_TokenStatsUpdateFooter is the discriminating test for
// "Goal: no status line details". A goal continuation turn drives the agent
// via agentManagerRunner.Run → agent.Run, NOT via SendUserInput — but the
// agent emits EventTokenStats/EventContextStats regardless, and those reach
// handleAgentOutputEvent → handleTokenStats → footer. This proves a
// goal-driven token-stats event updates the footer's context/cache display.
func TestGoalTurn_TokenStatsUpdateFooter(t *testing.T) {
	app := New(testSubsystems())

	// Simulate the stats events a goal continuation turn produces (same event
	// path as a user turn: agent emits, forwardEvent delivers).
	app.handleAgentOutputEvent(&agentic.OutputEvent{
		Type: agentic.EventTokenStats,
		Timings: &agentic.TokenTimings{
			PromptN:         12000,
			PredictedN:      800,
			CacheReadTokens: 4000,
		},
	})
	app.handleAgentOutputEvent(&agentic.OutputEvent{
		Type: agentic.EventContextStats,
		ContextStats: &agentic.ContextStats{
			MaxTokens:       32768,
			EstimatedTokens: 13000,
		},
	})

	stats := app.subs.footer.Data().Stats
	if stats == "" {
		t.Fatal("footer Stats empty after goal-turn token stats — status line not updating")
	}
	// The footer must show context usage (12-13K tokens / 32K window ≈ 40%).
	if !strings.Contains(stats, "32.") && !strings.Contains(stats, "%") {
		t.Fatalf("footer Stats missing context-window detail: %q", stats)
	}
	t.Logf("footer stats after goal turn: %q", stats)
}

// TestEventContextStats_FooterShowsProjected is P20/CX8 acceptance criterion 2
// at the app level: an EventContextStats carrying a projected figure must flow
// into the footer's occupancy display. The projection (80% of the window) must
// be what the footer renders — not the stale estimate (25%).
func TestEventContextStats_FooterShowsProjected(t *testing.T) {
	app := New(testSubsystems())
	app.handleAgentOutputEvent(&agentic.OutputEvent{
		Type: agentic.EventContextStats,
		ContextStats: &agentic.ContextStats{
			MaxTokens:       10000,
			EstimatedTokens: 2500, // stale estimate: 25%
			ProjectedTokens: 8000, // projection: 80%
		},
	})

	stats := app.subs.footer.Data().Stats
	if !strings.Contains(stats, "80.0%/10.0K") {
		t.Errorf("footer must show the projected figure (80.0%%/10.0K), got %q", stats)
	}
	if strings.Contains(stats, "25.0%") {
		t.Errorf("footer shows the stale estimate (25.0%%) instead of the projection, got %q", stats)
	}
}
