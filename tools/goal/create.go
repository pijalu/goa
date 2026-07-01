// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"encoding/json"
	"fmt"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/agentic"
)

// CreateGoalTool lets the model start an explicit goal.
type CreateGoalTool struct {
	Mode *goal.GoalMode
}

// Schema returns the tool schema.
func (t *CreateGoalTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "CreateGoal",
		Description: CreateGoalDescription(),
		Schema: map[string]any{
			"type":     "object",
			"required": []string{"objective"},
			"properties": map[string]any{
				"objective": map[string]any{
					"type":        "string",
					"minLength":   1,
					"description": "The objective to pursue. Must have a verifiable end state.",
				},
				"completionCriterion": map[string]any{
					"type":        "string",
					"description": "How to verify the goal is complete. Include when the user provides one.",
				},
				"replace": map[string]any{
					"type":        "boolean",
					"description": "Replace an existing active or paused goal instead of failing.",
				},
			},
			"additionalProperties": false,
		},
	}
}

// Execute parses the input and creates a goal.
func (t *CreateGoalTool) Execute(input string) (string, error) {
	var args struct {
		Objective           string  `json:"objective"`
		CompletionCriterion *string `json:"completionCriterion,omitempty"`
		Replace             bool    `json:"replace,omitempty"`
	}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return "", goalToolErr("CreateGoal", "invalid_input", fmt.Errorf("invalid CreateGoal input: %w", err))
	}

	snapshot, err := t.Mode.CreateGoal(goal.CreateGoalInput{
		Objective:           args.Objective,
		CompletionCriterion: args.CompletionCriterion,
		Replace:             args.Replace,
	}, goal.GoalActorModel)
	if err != nil {
		return "", goalToolErr("CreateGoal", "create_failed", err)
	}

	out, _ := json.Marshal(map[string]any{
		"goal": goal.ForModel(snapshot),
	})
	return string(out), nil
}

// IsRetryable reports whether the error is transient.
func (t *CreateGoalTool) IsRetryable(err error) bool { return false }
