// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"encoding/json"
	"fmt"
	"math"
	"strings"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/agentic"
)

const (
	minReasonableTimeBudgetMs = 1_000
	maxReasonableTimeBudgetMs = 24 * 60 * 60 * 1000
)

// SetGoalBudgetTool lets the model set a hard budget limit.
type SetGoalBudgetTool struct {
	Mode *goal.GoalMode
}

// Schema returns the tool schema.
func (t *SetGoalBudgetTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "SetGoalBudget",
		Description: SetGoalBudgetDescription(),
		Schema: map[string]any{
			"type":     "object",
			"required": []string{"value", "unit"},
			"properties": map[string]any{
				"value": map[string]any{
					"type":             "number",
					"exclusiveMinimum": 0,
					"description":      "The positive numeric budget value.",
				},
				"unit": map[string]any{
					"type":        "string",
					"enum":        []string{"turns", "tokens", "milliseconds", "seconds", "minutes", "hours"},
					"description": "The unit for the budget value.",
				},
			},
			"additionalProperties": false,
		},
	}
}

// Execute parses the input and sets the budget.
func (t *SetGoalBudgetTool) Execute(input string) (string, error) {
	var args struct {
		Value float64 `json:"value"`
		Unit  string  `json:"unit"`
	}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", goalToolErr("SetGoalBudget", "invalid_input", fmt.Errorf("invalid SetGoalBudget input: %w", err))
	}

	normalized := normalizeBudgetValue(args.Value, args.Unit)
	limits, ok := budgetLimitsFromInput(normalized, args.Unit)
	if !ok {
		return fmt.Sprintf("Goal budget not set: %s is not a reasonable goal budget.", formatBudget(normalized, args.Unit)), nil
	}

	if _, err := t.Mode.SetBudgetLimits(limits, goal.GoalActorModel); err != nil {
		return "", goalToolErr("SetGoalBudget", "set_budget_failed", err)
	}
	return fmt.Sprintf("Goal budget set: %s.", formatBudget(normalized, args.Unit)), nil
}

// IsRetryable reports whether the error is transient.
func (t *SetGoalBudgetTool) IsRetryable(err error) bool { return false }

func normalizeBudgetValue(value float64, unit string) float64 {
	switch unit {
	case "turns", "tokens":
		return math.Max(1, math.Round(value))
	default:
		return value
	}
}

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

func formatFloat(f float64) string {
	if f == math.Trunc(f) {
		return fmt.Sprintf("%d", int64(f))
	}
	return fmt.Sprintf("%g", f)
}
