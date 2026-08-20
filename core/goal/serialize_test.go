// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
	"testing"
)

func TestForModel_StripsGoalID(t *testing.T) {
	snap := GoalSnapshot{GoalID: "goal-1", Objective: "x"}
	got := ForModel(snap)
	if got.GoalID != "" {
		t.Errorf("GoalID = %q", got.GoalID)
	}
	if got.Objective != "x" {
		t.Errorf("Objective = %q", got.Objective)
	}
}

func TestResultForModel_NilGoal(t *testing.T) {
	got := ResultForModel(GoalToolResult{Goal: nil})
	if got.Goal != nil {
		t.Error("expected nil goal")
	}
}

func TestResultForModel_StripsGoalID(t *testing.T) {
	got := ResultForModel(GoalToolResult{Goal: &GoalSnapshot{GoalID: "goal-1", Objective: "x"}})
	if got.Goal == nil || got.Goal.GoalID != "" {
		t.Errorf("GoalID = %q", got.Goal.GoalID)
	}
}

// TestExcerpt verifies the rune-based truncation used to bound context
// injection: short strings pass through unchanged, long ones are cut with an
// ellipsis, and multi-byte runes are never split.
func TestExcerpt(t *testing.T) {
	if got := Excerpt("short", 10); got != "short" {
		t.Errorf("short string changed: %q", got)
	}
	long := "abcdefghij" // 10 runes
	if got := Excerpt(long, 4); got != "abcd…" {
		t.Errorf("truncated = %q, want %q", got, "abcd…")
	}
	// Multi-byte runes count as one; never split a rune.
	mb := "αβγδεζηθικ" // 10 Greek runes, 2 bytes each
	if got := Excerpt(mb, 4); got != "αβγδ…" {
		t.Errorf("multibyte truncated = %q, want %q", got, "αβγδ…")
	}
	if got := Excerpt("anything", 0); got != "" {
		t.Errorf("max<=0 must yield empty: %q", got)
	}
}

// TestSummarizeSnapshot_BoundsFields verifies the compact list form keeps
// identity/counters but bounds the objective and todo titles and drops the
// large free-text fields entirely.
func TestSummarizeSnapshot_BoundsFields(t *testing.T) {
	long := strings.Repeat("x", ExcerptObjectiveLen*3)
	snap := GoalSnapshot{
		GoalID:    "g1",
		Name:      "happy.fox",
		Status:    GoalActive,
		Objective: long,
		TurnsUsed: 3,
		Todos: []GoalTodoItem{
			{ID: "t1", Title: strings.Repeat("t", ExcerptFieldLen*2), Status: TodoDone},
		},
	}
	sum := SummarizeSnapshot(snap)
	if sum.Name != "happy.fox" || sum.Status != GoalActive || sum.TurnsUsed != 3 {
		t.Errorf("identity/counters lost: %+v", sum)
	}
	if len([]rune(sum.Objective)) > ExcerptObjectiveLen+1 {
		t.Errorf("objective not bounded: %d runes", len([]rune(sum.Objective)))
	}
	if len(sum.Todos) != 1 || len([]rune(sum.Todos[0].Title)) > ExcerptFieldLen+1 {
		t.Errorf("todo title not bounded: %+v", sum.Todos)
	}
}
