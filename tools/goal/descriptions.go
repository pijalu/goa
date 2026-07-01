// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import promptgoal "github.com/pijalu/goa/prompts/goal"

// CreateGoalDescription returns the LLM-facing description for CreateGoal.
func CreateGoalDescription() string { return promptgoal.CreateGoalDescription() }

// UpdateGoalDescription returns the LLM-facing description for UpdateGoal.
func UpdateGoalDescription() string { return promptgoal.UpdateGoalDescription() }

// GetGoalDescription returns the LLM-facing description for GetGoal.
func GetGoalDescription() string { return promptgoal.GetGoalDescription() }

// SetGoalBudgetDescription returns the LLM-facing description for SetGoalBudget.
func SetGoalBudgetDescription() string { return promptgoal.SetGoalBudgetDescription() }
