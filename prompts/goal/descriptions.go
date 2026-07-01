// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import _ "embed"

//go:embed create-goal.md
var createGoalDescription string

//go:embed update-goal.md
var updateGoalDescription string

//go:embed get-goal.md
var getGoalDescription string

//go:embed set-goal-budget.md
var setGoalBudgetDescription string

// CreateGoalDescription returns the LLM-facing description for CreateGoal.
func CreateGoalDescription() string { return createGoalDescription }

// UpdateGoalDescription returns the LLM-facing description for UpdateGoal.
func UpdateGoalDescription() string { return updateGoalDescription }

// GetGoalDescription returns the LLM-facing description for GetGoal.
func GetGoalDescription() string { return getGoalDescription }

// SetGoalBudgetDescription returns the LLM-facing description for SetGoalBudget.
func SetGoalBudgetDescription() string { return setGoalBudgetDescription }
