// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"context"
	"strings"
	"testing"
)

// countHistoryOccurrences returns how many history messages contain sub.
func countHistoryOccurrences(msgs []Message, sub string) int {
	n := 0
	for _, m := range msgs {
		if strings.Contains(m.Content, sub) {
			n++
		}
	}
	return n
}

// TestPersistGoalReminder_StaticPersistedOnceWhenUnchanged covers the E5
// enhancement (ENHANCE.md): the static goal reminder is byte-identical for a
// given goal across turns (see BuildStaticGoalReminder's own contract), yet
// persistGoalReminder appended it to the append-only history on EVERY user
// turn — ~1.5KB of redundant guidance per turn accumulating in context
// (goal-tool chatter was ~17K tokens / 2.6M re-reads in one session). The
// static reminder must be persisted only when it CHANGES; the dynamic
// progress (which legitimately churns per turn) keeps updating each turn.
func TestPersistGoalReminder_StaticPersistedOnceWhenUnchanged(t *testing.T) {
	cap := registerCapturingProvider("goal-chatter")
	gp := &mockGoalProvider{
		staticReminder:  "STATIC GOAL GUIDANCE BLOCK",
		dynamicProgress: "Progress: 1 turns",
	}
	agent := NewAgent(Config{
		Model:             testModel(cap.api),
		SystemPrompt:      "system prompt",
		Logger:            NewLogger(Error),
		GoalStateProvider: gp,
	})

	ctx := context.Background()
	if err := agent.Run(ctx, "first user turn"); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	gp.dynamicProgress = "Progress: 2 turns" // dynamic churns each turn
	if err := agent.Run(ctx, "second user turn"); err != nil {
		t.Fatalf("Run 2: %v", err)
	}

	hist := agent.GetHistory()
	staticCount := countHistoryOccurrences(hist, "STATIC GOAL GUIDANCE BLOCK")
	if staticCount != 1 {
		t.Errorf("static reminder persisted %d times across 2 turns, want 1 (byte-identical text must not be re-appended)", staticCount)
	}
	// Dynamic progress must appear for BOTH turns (each turn's fresh value).
	if countHistoryOccurrences(hist, "Progress: 1 turns") != 1 {
		t.Errorf("turn-1 dynamic progress missing from history")
	}
	if countHistoryOccurrences(hist, "Progress: 2 turns") != 1 {
		t.Errorf("turn-2 dynamic progress missing from history (dynamic must keep updating per turn)")
	}
}

// TestPersistGoalReminder_StaticRepersistedOnChange verifies the static
// reminder IS persisted again when its content actually changes (e.g. a new
// goal or an edited objective) — the dedup must not freeze stale guidance.
func TestPersistGoalReminder_StaticRepersistedOnChange(t *testing.T) {
	cap := registerCapturingProvider("goal-chatter-change")
	gp := &mockGoalProvider{
		staticReminder:  "GOAL V1 GUIDANCE",
		dynamicProgress: "Progress: 1 turns",
	}
	agent := NewAgent(Config{
		Model:             testModel(cap.api),
		SystemPrompt:      "system prompt",
		Logger:            NewLogger(Error),
		GoalStateProvider: gp,
	})

	ctx := context.Background()
	if err := agent.Run(ctx, "first user turn"); err != nil {
		t.Fatalf("Run 1: %v", err)
	}
	gp.staticReminder = "GOAL V2 GUIDANCE" // goal/objective changed
	if err := agent.Run(ctx, "second user turn"); err != nil {
		t.Fatalf("Run 2: %v", err)
	}

	hist := agent.GetHistory()
	if countHistoryOccurrences(hist, "GOAL V1 GUIDANCE") != 1 {
		t.Errorf("V1 static reminder should be present once")
	}
	if countHistoryOccurrences(hist, "GOAL V2 GUIDANCE") != 1 {
		t.Errorf("changed static reminder must be re-persisted (V2 missing)")
	}
}
