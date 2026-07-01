// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
	"testing"
)

func TestComputeBudgetReport_NoLimits(t *testing.T) {
	report := ComputeBudgetReport(GoalBudgetLimits{}, 0, 0, 0)
	if report.OverBudget {
		t.Error("should not be over budget with no limits")
	}
}

func TestComputeBudgetReport_TurnBudget(t *testing.T) {
	limit := 5
	report := ComputeBudgetReport(GoalBudgetLimits{TurnBudget: &limit}, 3, 0, 0)
	if report.OverBudget {
		t.Error("3/5 should not be over budget")
	}
	if report.RemainingTurns == nil || *report.RemainingTurns != 2 {
		t.Errorf("remaining = %v", report.RemainingTurns)
	}
}

func TestComputeBudgetReport_TurnBudgetReached(t *testing.T) {
	limit := 5
	report := ComputeBudgetReport(GoalBudgetLimits{TurnBudget: &limit}, 5, 0, 0)
	if !report.OverBudget {
		t.Error("5/5 should be over budget")
	}
}

func TestComputeBudgetReport_TokenBudget(t *testing.T) {
	limit := 1000
	report := ComputeBudgetReport(GoalBudgetLimits{TokenBudget: &limit}, 0, 1001, 0)
	if !report.OverBudget {
		t.Error("1001/1000 should be over budget")
	}
	if report.RemainingTokens == nil || *report.RemainingTokens != 0 {
		t.Errorf("remaining = %v", report.RemainingTokens)
	}
}

func TestComputeBudgetReport_TimeBudget(t *testing.T) {
	var limit int64 = 1000
	report := ComputeBudgetReport(GoalBudgetLimits{WallClockBudgetMs: &limit}, 0, 0, 2000)
	if !report.OverBudget {
		t.Error("2000/1000 should be over budget")
	}
}

func TestMaxBudgetFraction(t *testing.T) {
	limit := 10
	snap := GoalSnapshot{
		TurnsUsed: 3,
		Budget:    GoalBudgetReport{TurnBudget: &limit},
	}
	if got := MaxBudgetFraction(snap); got != 0.3 {
		t.Errorf("MaxBudgetFraction = %v", got)
	}
}

func TestMaxBudgetFraction_NoBudgets(t *testing.T) {
	if got := MaxBudgetFraction(GoalSnapshot{}); got != 0 {
		t.Errorf("MaxBudgetFraction = %v", got)
	}
}

func TestBudgetBandGuidance(t *testing.T) {
	limit := 10
	snap := GoalSnapshot{
		TurnsUsed: 8,
		Budget:    GoalBudgetReport{TurnBudget: &limit},
	}
	if got := BudgetBandGuidance(snap); !strings.Contains(got, "nearing a budget") {
		t.Errorf("guidance = %q", got)
	}
}
