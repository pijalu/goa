// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"fmt"
	"math"
	"strings"

	"github.com/pijalu/goa/core/goal"
)

const (
	minReasonableTimeBudgetMs = 1_000
	maxReasonableTimeBudgetMs = 24 * 60 * 60 * 1000
)

// normalizeBudgetValue rounds integral units (turns, tokens) to whole numbers.
func normalizeBudgetValue(value float64, unit string) float64 {
	switch unit {
	case "turns", "tokens":
		return math.Max(1, math.Round(value))
	default:
		return value
	}
}

// budgetLimitsFromInput converts a value+unit into GoalBudgetLimits, reporting
// false when the unit is unknown or the time budget is outside a reasonable range.
func budgetLimitsFromInput(value float64, unit string) (goal.GoalBudgetLimits, bool) {
	switch unit {
	case "turns":
		v := int(value)
		return goal.GoalBudgetLimits{TurnBudget: &v}, true
	case "tokens":
		v := int(value)
		return goal.GoalBudgetLimits{TokenBudget: &v}, true
	case "milliseconds", "seconds", "minutes", "hours":
		ms := int64(math.Round(toMilliseconds(value, unit)))
		if ms < minReasonableTimeBudgetMs || ms > maxReasonableTimeBudgetMs {
			return goal.GoalBudgetLimits{}, false
		}
		return goal.GoalBudgetLimits{WallClockBudgetMs: &ms}, true
	default:
		return goal.GoalBudgetLimits{}, false
	}
}

// toMilliseconds converts a time value in the given unit to milliseconds.
func toMilliseconds(value float64, unit string) float64 {
	switch unit {
	case "milliseconds":
		return value
	case "seconds":
		return value * 1000
	case "minutes":
		return value * 60 * 1000
	case "hours":
		return value * 60 * 60 * 1000
	default:
		return value
	}
}

// formatBudget renders a value+unit as a concise human-readable string.
func formatBudget(value float64, unit string) string {
	singular := unit
	if strings.HasSuffix(unit, "s") {
		singular = unit[:len(unit)-1]
	}
	if value == 1 {
		return fmt.Sprintf("1 %s", singular)
	}
	return fmt.Sprintf("%s %s", formatFloat(value), unit)
}

// formatFloat formats a float, dropping the decimal for whole numbers.
func formatFloat(f float64) string {
	if f == math.Trunc(f) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}
