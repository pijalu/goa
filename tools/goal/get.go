// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"encoding/json"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/agentic"
)

// GetGoalTool returns the current goal snapshot.
type GetGoalTool struct {
	Mode *goal.GoalMode
}

// Schema returns the tool schema.
func (t *GetGoalTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "GetGoal",
		Description: GetGoalDescription(),
		Schema: map[string]any{
			"type":                 "object",
			"properties":           map[string]any{},
			"additionalProperties": false,
		},
	}
}

// Execute returns the current goal with goalId stripped.
func (t *GetGoalTool) Execute(input string) (string, error) {
	result := goal.ResultForModel(t.Mode.GetGoal())
	out, _ := json.Marshal(result)
	return string(out), nil
}

// IsRetryable reports whether the error is transient.
func (t *GetGoalTool) IsRetryable(err error) bool { return false }
