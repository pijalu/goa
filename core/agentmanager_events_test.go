// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"testing"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/event"
)

// newEventsTestManager builds a minimal AgentManager for recorder-event tests.
func newEventsTestManager() *AgentManager {
	return NewAgentManager(&config.Config{}, nil, nil, nil, event.MakeBus(10, 10, 10, 10), "")
}

// TestAgentManager_TokenStatsEventsBecomeCompletions pins the bugs.md §1 fix
// at the event layer: each EventTokenStats of the main agent (one per LLM API
// call of a turn's tool loop) appends one per-call completion record, while
// the turn record itself still flattens to a single usage snapshot.
func TestAgentManager_TokenStatsEventsBecomeCompletions(t *testing.T) {
	am := newEventsTestManager()
	am.turnRecorder.ResetTurn(time.Now())

	// Round 1 of the tool loop: cold call (write only).
	am.handleTypedEvent(agentic.OutputEvent{
		Type: agentic.EventTokenStats,
		Timings: &agentic.TokenTimings{
			PromptN: 100, PredictedN: 10, CacheWriteTokens: 200,
		},
	})
	// Round 2: warm call (reads the round-1 prefix).
	am.handleTypedEvent(agentic.OutputEvent{
		Type: agentic.EventTokenStats,
		Timings: &agentic.TokenTimings{
			PromptN: 50, PredictedN: 5, CacheReadTokens: 250,
		},
	})

	comps := am.CompletionHistory()
	if len(comps) != 2 {
		t.Fatalf("completions = %d, want 2 (one per API call)", len(comps))
	}
	for i, want := range []struct{ read, write int }{{0, 200}, {250, 0}} {
		c := comps[i]
		if c.AgentRole != "main" || c.TurnNumber != 1 {
			t.Errorf("completion[%d] = %+v, want role main turn 1", i, c)
		}
		if c.CacheRead != want.read || c.CacheWrite != want.write {
			t.Errorf("completion[%d] usage = %d/%d, want %d/%d (per-call, not flattened)",
				i, c.CacheRead, c.CacheWrite, want.read, want.write)
		}
	}

	// The in-progress turn snapshot keeps its flattened contract (last call).
	cur := am.CurrentTurn()
	if cur == nil || cur.TokenUsage.CacheRead != 250 {
		t.Errorf("CurrentTurn = %+v, want last-call snapshot CacheRead=250", cur)
	}

	// Finalizing the turn must not duplicate or drop completions, and the next
	// turn's calls carry the next turn number.
	am.turnRecorder.FinalizeTurn(nil, "")
	am.turnRecorder.ResetTurn(time.Now())
	am.handleTypedEvent(agentic.OutputEvent{
		Type:    agentic.EventTokenStats,
		Timings: &agentic.TokenTimings{PromptN: 7, CacheReadTokens: 300},
	})
	comps = am.CompletionHistory()
	if len(comps) != 3 {
		t.Fatalf("completions after finalize = %d, want 3", len(comps))
	}
	if comps[2].TurnNumber != 2 {
		t.Errorf("third completion turn = %d, want 2 (shared sequence)", comps[2].TurnNumber)
	}
}

// TestAgentManager_TextOnlyCollapseEventMarksCompletion pins the event-layer
// wiring of the cache-miss shape classification (bugs.md 2026-08-30): an
// EventTokenStats carrying TextOnlyCollapse (the P7 no-tools round) makes its
// OWN completion record carry TextOnlyCollapse — and no other completion.
func TestAgentManager_TextOnlyCollapseEventMarksCompletion(t *testing.T) {
	am := newEventsTestManager()
	am.turnRecorder.ResetTurn(time.Now())

	// Round 1: normal tools-present call with an established prefix.
	am.handleTypedEvent(agentic.OutputEvent{
		Type:    agentic.EventTokenStats,
		Timings: &agentic.TokenTimings{PromptN: 80768, CacheReadTokens: 80768},
	})
	// Round 2: the P7 text-only collapse (no tools, tool_choice none) —
	// the by-design bust shape from the RCA export (read 80,768 → 0).
	am.handleTypedEvent(agentic.OutputEvent{
		Type:             agentic.EventTokenStats,
		Timings:          &agentic.TokenTimings{PromptN: 80773},
		TextOnlyCollapse: true,
	})
	// Round 3: a later normal call.
	am.handleTypedEvent(agentic.OutputEvent{
		Type:    agentic.EventTokenStats,
		Timings: &agentic.TokenTimings{PromptN: 10, CacheReadTokens: 500},
	})

	comps := am.CompletionHistory()
	if len(comps) != 3 {
		t.Fatalf("completions = %d, want 3", len(comps))
	}
	if comps[0].TextOnlyCollapse || comps[2].TextOnlyCollapse {
		t.Error("normal rounds must not carry TextOnlyCollapse")
	}
	if !comps[1].TextOnlyCollapse {
		t.Error("collapse round's completion must carry TextOnlyCollapse")
	}
	if comps[1].ContextReset {
		t.Error("a collapse is a request-shape change, not a context reset: ContextReset must stay false")
	}
}
