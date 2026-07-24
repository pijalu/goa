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

// GoalTool is the single goal-management tool exposed to the model. It
// consolidates create / update / get / set_budget behind one `action`
// dispatcher so the tool array stays small and stable for prompt caching
// (bugs.md S2): one fixed schema instead of four.
type GoalTool struct {
	Mode       *goal.GoalMode
	ReminderFn func(string)
	// CreateAllowed reports whether autonomous goal creation is permitted. It
	// gates only the `create` action and only when NO goal is currently active
	// (bugs.md S2: all goal actions are allowed while a goal is running).
	CreateAllowed func() bool
}

// goalArgs is the union of all per-action fields. `action` selects which
// subset is meaningful; the rest are validated per action in Execute.
type goalArgs struct {
	Action              string   `json:"action"`
	Objective           string   `json:"objective,omitempty"`
	CompletionCriterion *string  `json:"completionCriterion,omitempty"`
	Replace             bool     `json:"replace,omitempty"`
	Status              string   `json:"status,omitempty"`
	Value               *float64 `json:"value,omitempty"`
	Unit                string   `json:"unit,omitempty"`
}

// Schema returns the tool schema.
func (t *GoalTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "goal",
		Description: GoalDescription(),
		Schema: map[string]any{
			"type":     "object",
			"required": []string{"action"},
			"properties": map[string]any{
				"action": map[string]any{
					"type":        "string",
					"enum":        []string{"create", "update", "get", "set_budget"},
					"description": "The goal operation to perform.",
				},
				"objective": map[string]any{
					"type":        "string",
					"description": "create: the objective to pursue (must have a verifiable end state).",
				},
				"completionCriterion": map[string]any{
					"type":        "string",
					"description": "create: how to verify the goal is complete.",
				},
				"replace": map[string]any{
					"type":        "boolean",
					"description": "create: replace an existing goal instead of failing.",
				},
				"status": map[string]any{
					"type":        "string",
					"enum":        []string{"active", "complete", "paused", "blocked"},
					"description": "update: the lifecycle status to set.",
				},
				"value": map[string]any{
					"type":        "number",
					"description": "set_budget: the positive numeric budget value.",
				},
				"unit": map[string]any{
					"type":        "string",
					"enum":        []string{"turns", "tokens", "milliseconds", "seconds", "minutes", "hours"},
					"description": "set_budget: the unit for the budget value.",
				},
			},
			"additionalProperties": false,
		},
	}
}

// Execute parses the input and dispatches to the action handler.
func (t *GoalTool) Execute(input string) (string, error) {
	res, err := t.ExecuteWithResult(input)
	if err != nil {
		return "", err
	}
	return res.Output, nil
}

// ExecuteWithResult implements agentic.ResultTool and carries the StopTurn
// signal for terminal update statuses.
func (t *GoalTool) ExecuteWithResult(input string) (agentic.ToolResult, error) {
	var args goalArgs
	if err := json.Unmarshal([]byte(input), &args); err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input", fmt.Errorf("invalid goal input: %w", err))
	}
	switch args.Action {
	case "create":
		return t.handleCreate(args)
	case "update":
		return t.handleUpdate(args)
	case "get":
		return t.handleGet()
	case "set_budget":
		return t.handleSetBudget(args)
	default:
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_action",
			fmt.Errorf("invalid goal action %q: must be create, update, get, or set_budget", args.Action))
	}
}

func (t *GoalTool) handleCreate(args goalArgs) (agentic.ToolResult, error) {
	if args.Objective == "" {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("action \"create\" requires a non-empty \"objective\""))
	}
	// Execution-time gate (bugs.md S2): autonomous creation is blocked only
	// when the feature flag is off AND no goal is active. All actions are
	// allowed while a goal is running.
	if t.CreateAllowed != nil && !t.CreateAllowed() {
		return agentic.ToolResult{}, goalToolErr("goal", "create_disabled",
			fmt.Errorf("autonomous goal creation is disabled; the user can start one with /goal"))
	}
	snapshot, err := t.Mode.CreateGoal(goal.CreateGoalInput{
		Objective:           args.Objective,
		CompletionCriterion: args.CompletionCriterion,
		Replace:             args.Replace,
	}, goal.GoalActorModel)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "create_failed", err)
	}
	out, _ := json.Marshal(map[string]any{"goal": goal.ForModel(snapshot)})
	return agentic.ToolResult{Output: string(out)}, nil
}

func (t *GoalTool) handleUpdate(args goalArgs) (agentic.ToolResult, error) {
	if args.Status == "" {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("action \"update\" requires \"status\" (active|complete|paused|blocked)"))
	}
	handlers := map[string]func() (agentic.ToolResult, error){
		"active":   t.updateActive,
		"paused":   t.updatePaused,
		"blocked":  t.updateBlocked,
		"complete": t.updateComplete,
	}
	handler, ok := handlers[args.Status]
	if !ok {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_status",
			fmt.Errorf("invalid goal status %q: must be active, complete, paused, or blocked", args.Status))
	}
	res, err := handler()
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "update_failed", err)
	}
	return res, nil
}

func (t *GoalTool) updateActive() (agentic.ToolResult, error) {
	if _, err := t.Mode.ResumeGoal(goal.GoalReasonInput{}, goal.GoalActorModel); err != nil {
		return agentic.ToolResult{}, err
	}
	return agentic.ToolResult{Output: "Goal resumed."}, nil
}

func (t *GoalTool) updatePaused() (agentic.ToolResult, error) {
	if _, err := t.Mode.PauseGoal(goal.GoalReasonInput{}, goal.GoalActorModel); err != nil {
		return agentic.ToolResult{}, err
	}
	return agentic.ToolResult{Output: "Goal paused.", StopTurn: true}, nil
}

func (t *GoalTool) updateBlocked() (agentic.ToolResult, error) {
	blocked, err := t.Mode.MarkBlocked(goal.GoalReasonInput{}, goal.GoalActorModel)
	if err != nil {
		return agentic.ToolResult{}, err
	}
	if blocked != nil && t.ReminderFn != nil {
		t.ReminderFn(goal.BuildBlockedReasonPrompt(*blocked))
	}
	return agentic.ToolResult{Output: "Goal marked blocked.", StopTurn: true}, nil
}

func (t *GoalTool) updateComplete() (agentic.ToolResult, error) {
	completed, err := t.Mode.MarkComplete(goal.GoalReasonInput{}, goal.GoalActorModel)
	if err != nil {
		return agentic.ToolResult{}, err
	}
	if completed != nil && t.ReminderFn != nil {
		t.ReminderFn(goal.BuildCompletionSummary(*completed))
	}
	return agentic.ToolResult{Output: "Goal marked complete.", StopTurn: true}, nil
}

func (t *GoalTool) handleGet() (agentic.ToolResult, error) {
	result := goal.ResultForModel(t.Mode.GetGoal())
	out, _ := json.Marshal(result)
	return agentic.ToolResult{Output: string(out)}, nil
}

func (t *GoalTool) handleSetBudget(args goalArgs) (agentic.ToolResult, error) {
	if args.Value == nil || args.Unit == "" {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("action \"set_budget\" requires \"value\" (number) and \"unit\""))
	}
	normalized := normalizeBudgetValue(*args.Value, args.Unit)
	limits, ok := budgetLimitsFromInput(normalized, args.Unit)
	if !ok {
		return agentic.ToolResult{Output: fmt.Sprintf("Goal budget not set: %s is not a reasonable goal budget.", formatBudget(normalized, args.Unit))}, nil
	}
	if _, err := t.Mode.SetBudgetLimits(limits, goal.GoalActorModel); err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "set_budget_failed", err)
	}
	return agentic.ToolResult{Output: fmt.Sprintf("Goal budget set: %s.", formatBudget(normalized, args.Unit))}, nil
}

// IsRetryable reports whether the error is transient.
func (t *GoalTool) IsRetryable(err error) bool { return false }
