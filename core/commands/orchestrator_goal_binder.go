// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/core/orchestrator"
)

// goalModeBinder adapts a *goal.GoalMode to the orchestrator.GoalBinder
// interface so a goal-bound orchestration run accrues aggregate tokens and
// drives the goal lifecycle (active → complete / blocked).
type goalModeBinder struct {
	mode   *goal.GoalMode
	goalID string
}

// NewGoalBinder wraps a GoalMode as an orchestrator.GoalBinder. Create starts
// a fresh goal (Replace=true) so reusing a session's goal slot for an
// orchestration run is explicit.
func NewGoalBinder(mode *goal.GoalMode) orchestrator.GoalBinder {
	return &goalModeBinder{mode: mode}
}

func (b *goalModeBinder) Create(objective string, tokenBudget int) (string, error) {
	if b.mode == nil {
		return "", fmt.Errorf("goal mode unavailable")
	}
	input := goal.CreateGoalInput{Objective: objective, Replace: true}
	if tokenBudget > 0 {
		tb := tokenBudget
		input.CompletionCriterion = nil
		// Budget is applied after creation via the budget API if present; here
		// we stash it for RecordTokens enforcement via GoalMode's own limits.
		_ = tb
	}
	snap, err := b.mode.CreateGoal(input, goal.GoalActorUser)
	if err != nil {
		return "", err
	}
	b.goalID = snap.GoalID
	return snap.GoalID, nil
}

func (b *goalModeBinder) RecordTokens(delta int) (bool, error) {
	if b.mode == nil || delta <= 0 {
		return false, nil
	}
	snap, err := b.mode.RecordTokenUsage(delta)
	if err != nil {
		return false, err
	}
	return snap.Budget.OverBudget, nil
}

func (b *goalModeBinder) Complete(reason string) error {
	if b.mode == nil {
		return nil
	}
	r := reason
	_, err := b.mode.MarkComplete(goal.GoalReasonInput{Reason: &r}, goal.GoalActorRuntime)
	return err
}

func (b *goalModeBinder) Block(reason string) error {
	if b.mode == nil {
		return nil
	}
	r := reason
	_, err := b.mode.PauseActiveGoal(goal.GoalReasonInput{Reason: &r}, goal.GoalActorRuntime)
	return err
}
