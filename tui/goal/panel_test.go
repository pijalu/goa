// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"testing"

	"github.com/pijalu/goa/core/goal"
)

func TestPanel_Render(t *testing.T) {
	p := NewPanel()
	limit := 10
	snap := &goal.GoalSnapshot{
		Objective:   "fix the failing tests in the auth package",
		Status:      goal.GoalActive,
		TurnsUsed:   3,
		TokensUsed:  1200,
		WallClockMs: 65000,
		Budget: goal.GoalBudgetReport{
			TurnBudget: &limit,
		},
	}
	p.SetSnapshot(snap)
	lines := p.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected lines")
	}
	found := false
	for _, line := range lines {
		if contains(line, "auth package") {
			found = true
		}
	}
	if !found {
		t.Errorf("panel missing objective: %v", lines)
	}
}

func TestPanel_NoStopCondition(t *testing.T) {
	p := NewPanel()
	p.SetSnapshot(&goal.GoalSnapshot{Objective: "x", Status: goal.GoalActive})
	lines := p.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected lines")
	}
}

func TestPanel_WithBudget(t *testing.T) {
	p := NewPanel()
	limit := 10
	p.SetSnapshot(&goal.GoalSnapshot{
		Objective: "x",
		Status:    goal.GoalActive,
		Budget:    goal.GoalBudgetReport{TurnBudget: &limit},
	})
	lines := p.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected lines")
	}
}

func TestPanel_StatusWithReason(t *testing.T) {
	p := NewPanel()
	reason := "waiting"
	p.SetSnapshot(&goal.GoalSnapshot{
		Objective:      "x",
		Status:         goal.GoalBlocked,
		TerminalReason: &reason,
	})
	lines := p.Render(80)
	if len(lines) == 0 {
		t.Fatal("expected lines")
	}
}

func TestPanel_StatusColor(t *testing.T) {
	for _, status := range []goal.GoalStatus{goal.GoalActive, goal.GoalDone, goal.GoalBlocked, goal.GoalPaused} {
		p := NewPanel()
		p.SetSnapshot(&goal.GoalSnapshot{Objective: "x", Status: status})
		if p.Render(80) == nil {
			t.Errorf("status %s produced nil", status)
		}
	}
}

func TestPanel_NilSnapshot(t *testing.T) {
	p := NewPanel()
	if p.Render(80) != nil {
		t.Error("expected nil")
	}
}

func TestPanel_Narrow(t *testing.T) {
	p := NewPanel()
	p.SetSnapshot(&goal.GoalSnapshot{Objective: "x", Status: goal.GoalActive})
	lines := p.Render(5)
	if len(lines) == 0 {
		t.Fatal("expected lines")
	}
}

func TestPanel_Wrap(t *testing.T) {
	lines := wrapText("one two three four five six seven", 10)
	if len(lines) < 2 {
		t.Errorf("lines = %d", len(lines))
	}
}

func TestPanel_NoOps(t *testing.T) {
	p := NewPanel()
	p.HandleInput("x")
	p.Invalidate()
}
