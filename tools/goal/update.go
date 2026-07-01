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

// UpdateGoalTool lets the model control the goal lifecycle.
type UpdateGoalTool struct {
	Mode       *goal.GoalMode
	ReminderFn func(string)
}

// Schema returns the tool schema.
func (t *UpdateGoalTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "UpdateGoal",
		Description: UpdateGoalDescription(),
		Schema: map[string]any{
			"type":     "object",
			"required": []string{"status"},
			"properties": map[string]any{
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"active", "complete", "paused", "blocked"},
					"description": "The lifecycle status to set for the current goal.",
				},
			},
			"additionalProperties": false,
		},
	}
}

// Execute parses the input and updates the goal status.
// It wraps the result so the agent loop can stop after non-active statuses.
func (t *UpdateGoalTool) Execute(input string) (string, error) {
	res, err := t.ExecuteWithResult(input)
	if err != nil {
		return "", err
	}
	return res.Output, nil
}

// ExecuteWithResult implements agentic.ResultTool and carries the StopTurn signal.
func (t *UpdateGoalTool) ExecuteWithResult(input string) (agentic.ToolResult, error) {
	var args struct {
		Status string `json:"status"`
	}
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return agentic.ToolResult{}, goalToolErr("UpdateGoal", "invalid_input", fmt.Errorf("invalid UpdateGoal input: %w", err))
	}

	handlers := map[string]func() (agentic.ToolResult, error){
		"active":   t.handleActive,
		"paused":   t.handlePaused,
		"blocked":  t.handleBlocked,
		"complete": t.handleComplete,
	}
	handler, ok := handlers[args.Status]
	if !ok {
		return agentic.ToolResult{}, goalToolErr("UpdateGoal", "invalid_status", fmt.Errorf("invalid goal status %q", args.Status))
	}
	res, err := handler()
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("UpdateGoal", "update_failed", err)
	}
	return res, nil
}

func (t *UpdateGoalTool) handleActive() (agentic.ToolResult, error) {
	if _, err := t.Mode.ResumeGoal(goal.GoalReasonInput{}, goal.GoalActorModel); err != nil {
		return agentic.ToolResult{}, err
	}
	return agentic.ToolResult{Output: "Goal resumed."}, nil
}

func (t *UpdateGoalTool) handlePaused() (agentic.ToolResult, error) {
	if _, err := t.Mode.PauseGoal(goal.GoalReasonInput{}, goal.GoalActorModel); err != nil {
		return agentic.ToolResult{}, err
	}
	return agentic.ToolResult{Output: "Goal paused.", StopTurn: true}, nil
}

func (t *UpdateGoalTool) handleBlocked() (agentic.ToolResult, error) {
	blocked, err := t.Mode.MarkBlocked(goal.GoalReasonInput{}, goal.GoalActorModel)
	if err != nil {
		return agentic.ToolResult{}, err
	}
	if blocked != nil && t.ReminderFn != nil {
		t.ReminderFn(goal.BuildBlockedReasonPrompt(*blocked))
	}
	return agentic.ToolResult{Output: "Goal marked blocked.", StopTurn: true}, nil
}

func (t *UpdateGoalTool) handleComplete() (agentic.ToolResult, error) {
	completed, err := t.Mode.MarkComplete(goal.GoalReasonInput{}, goal.GoalActorModel)
	if err != nil {
		return agentic.ToolResult{}, err
	}
	if completed != nil && t.ReminderFn != nil {
		t.ReminderFn(goal.BuildCompletionSummary(*completed))
	}
	return agentic.ToolResult{Output: "Goal marked complete.", StopTurn: true}, nil
}

// IsRetryable reports whether the error is transient.
func (t *UpdateGoalTool) IsRetryable(err error) bool { return false }
