// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/goal"
)

func strPtr(s string) *string { return &s }

func TestGoalInjector_ActiveGoalReminder(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.CreateGoal(goal.CreateGoalInput{Objective: "fix tests"}, goal.GoalActorUser)
	inj := &GoalInjector{Mode: mode}
	rem := inj.ActiveGoalReminder()
	if !strings.Contains(rem, "fix tests") {
		t.Errorf("reminder missing objective: %s", rem)
	}
}

func TestGoalInjector_PausedGoalReminder(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.CreateGoal(goal.CreateGoalInput{Objective: "fix tests"}, goal.GoalActorUser)
	mode.PauseGoal(goal.GoalReasonInput{Reason: strPtr("paused")}, goal.GoalActorUser)
	inj := &GoalInjector{Mode: mode}
	rem := inj.ActiveGoalReminder()
	if !strings.Contains(rem, "paused") {
		t.Errorf("reminder missing paused: %s", rem)
	}
}

func TestGoalInjector_BlockedGoalReminder(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.CreateGoal(goal.CreateGoalInput{Objective: "fix tests"}, goal.GoalActorUser)
	mode.MarkBlocked(goal.GoalReasonInput{Reason: strPtr("blocker")}, goal.GoalActorUser)
	inj := &GoalInjector{Mode: mode}
	rem := inj.ActiveGoalReminder()
	if !strings.Contains(rem, "blocked") {
		t.Errorf("reminder missing blocked: %s", rem)
	}
}

func TestGoalInjector_NoGoal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	inj := &GoalInjector{Mode: mode}
	if inj.ActiveGoalReminder() != "" {
		t.Error("expected empty reminder")
	}
}

func TestGoalInjector_CompleteGoal(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mode.CreateGoal(goal.CreateGoalInput{Objective: "fix tests"}, goal.GoalActorUser)
	mode.CancelGoal(goal.GoalActorUser)
	inj := &GoalInjector{Mode: mode}
	if inj.ActiveGoalReminder() != "" {
		t.Error("expected empty reminder after cancel")
	}
}
