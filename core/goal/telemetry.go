// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

// Telemetry event name constants.
const (
	TelemetryGoalCreated       = "goal_created"
	TelemetryGoalContinued     = "goal_continued"
	TelemetryGoalStatusChanged = "goal_status_changed"
	TelemetryGoalBudgetSet     = "goal_budget_set"
	TelemetryGoalCleared       = "goal_cleared"
)

// BudgetTelemetryProperties returns telemetry-safe budget flags.
func BudgetTelemetryProperties(limits GoalBudgetLimits) map[string]any {
	return map[string]any{
		"has_token_budget":      limits.TokenBudget != nil,
		"has_turn_budget":       limits.TurnBudget != nil,
		"has_wall_clock_budget": limits.WallClockBudgetMs != nil,
	}
}
