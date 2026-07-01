// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import "testing"

func TestBudgetTelemetryProperties(t *testing.T) {
	limit := 10
	var ms int64 = 1000
	props := BudgetTelemetryProperties(GoalBudgetLimits{
		TokenBudget:       &limit,
		WallClockBudgetMs: &ms,
	})
	if props["has_token_budget"] != true {
		t.Error("token budget flag")
	}
	if props["has_turn_budget"] != false {
		t.Error("turn budget flag")
	}
	if props["has_wall_clock_budget"] != true {
		t.Error("wall clock budget flag")
	}
}
