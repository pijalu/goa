// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
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
