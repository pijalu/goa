// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import "time"

// ComputeBudgetReport computes the full budget report from limits and current usage.
func ComputeBudgetReport(limits GoalBudgetLimits, turnsUsed, tokensUsed int, wallClockMs int64) GoalBudgetReport {
	report := GoalBudgetReport{
		TokenBudget:       limits.TokenBudget,
		TurnBudget:        limits.TurnBudget,
		WallClockBudgetMs: limits.WallClockBudgetMs,
	}

	if limits.TokenBudget != nil {
		rem := *limits.TokenBudget - tokensUsed
		if rem < 0 {
			rem = 0
		}
		report.RemainingTokens = &rem
		if tokensUsed >= *limits.TokenBudget {
			report.OverBudget = true
		}
	}
	if limits.TurnBudget != nil {
		rem := *limits.TurnBudget - turnsUsed
		if rem < 0 {
			rem = 0
		}
		report.RemainingTurns = &rem
		if turnsUsed >= *limits.TurnBudget {
			report.OverBudget = true
		}
	}
	if limits.WallClockBudgetMs != nil {
		rem := *limits.WallClockBudgetMs - wallClockMs
		if rem < 0 {
			rem = 0
		}
		report.RemainingWallClockMs = &rem
		if wallClockMs >= *limits.WallClockBudgetMs {
			report.OverBudget = true
		}
	}

	return report
}

// LiveWallClockMs returns the effective wall-clock time, including the in-flight
// active interval when the goal is currently active.
func LiveWallClockMs(state goalStage, now time.Time) int64 {
	if state.status == GoalActive && state.wallClockResumedAt != nil {
		live := now.UnixMilli() - *state.wallClockResumedAt
		if live < 0 {
			live = 0
		}
		return state.wallClockMs + live
	}
	return state.wallClockMs
}

// MaxBudgetFraction returns the highest budget-usage fraction across all set hard
// budgets. Returns 0 when no budgets are set.
func MaxBudgetFraction(snapshot GoalSnapshot) float64 {
	var fractions []float64
	if snapshot.Budget.TurnBudget != nil && *snapshot.Budget.TurnBudget > 0 {
		fractions = append(fractions, float64(snapshot.TurnsUsed)/float64(*snapshot.Budget.TurnBudget))
	}
	if snapshot.Budget.TokenBudget != nil && *snapshot.Budget.TokenBudget > 0 {
		fractions = append(fractions, float64(snapshot.TokensUsed)/float64(*snapshot.Budget.TokenBudget))
	}
	if snapshot.Budget.WallClockBudgetMs != nil && *snapshot.Budget.WallClockBudgetMs > 0 {
		fractions = append(fractions, float64(snapshot.WallClockMs)/float64(*snapshot.Budget.WallClockBudgetMs))
	}
	if len(fractions) == 0 {
		return 0
	}
	max := fractions[0]
	for _, f := range fractions[1:] {
		if f > max {
			max = f
		}
	}
	return max
}

// BudgetBandGuidance returns a short guidance string based on MaxBudgetFraction.
func BudgetBandGuidance(snapshot GoalSnapshot) string {
	if MaxBudgetFraction(snapshot) >= 0.75 {
		return "Budget guidance: you are nearing a budget. Converge on the objective and avoid starting new discretionary work."
	}
	return "Budget guidance: you are within budget. Make steady, focused progress toward the objective."
}
