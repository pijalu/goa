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
	// Queue is the durable FIFO of upcoming goals. When set, the tool manages
	// goals as a todo-like list: `create` APPENDS to the queue by default when
	// a goal is already active (rather than failing), and the list/cancel/reorder
	// actions operate over the active goal + the queue. May be nil (queue-less
	// setups) — list actions then operate on the active goal only.
	Queue GoalQueue
}

// GoalQueue is the subset of the durable goal queue the tool needs. It is
// satisfied by *core.GoalQueueStore.
type GoalQueue interface {
	Read() ([]goal.UpcomingGoal, error)
	AppendWithOptions(objective string, freshContext bool) ([]goal.UpcomingGoal, error)
	Remove(id string) ([]goal.UpcomingGoal, *goal.UpcomingGoal, error)
	Move(id, direction string) ([]goal.UpcomingGoal, error)
}

// goalArgs is the union of all per-action fields. `action` selects which
// subset is meaningful; the rest are validated per action in Execute.
type goalArgs struct {
	Action              string   `json:"action"`
	Objective           string   `json:"objective,omitempty"`
	Objectives          []string `json:"objectives,omitempty"`
	CompletionCriterion *string  `json:"completionCriterion,omitempty"`
	Replace             bool     `json:"replace,omitempty"`
	FreshContext        bool     `json:"freshContext,omitempty"`
	Status              string   `json:"status,omitempty"`
	Value               *float64 `json:"value,omitempty"`
	Unit                string   `json:"unit,omitempty"`
	// Goal list management (todo-like): target a goal by id/name and reorder.
	GoalID    string `json:"goalId,omitempty"`
	Direction string `json:"direction,omitempty"`
	// Todo management (framework-managed todo list for the goal).
	TodoTitle  string `json:"todoTitle,omitempty"`
	TodoID     string `json:"todoId,omitempty"`
	TodoStatus string `json:"todoStatus,omitempty"`
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
					"enum":        []string{"create", "update", "get", "set_budget", "add_todo", "update_todo", "list", "cancel", "reorder"},
					"description": "The goal operation to perform.",
				},
				"objective": map[string]any{
					"type":        "string",
					"description": "create: the objective to pursue (must have a verifiable end state).",
				},
				"objectives": map[string]any{
					"type":        "array",
					"items":       map[string]any{"type": "string"},
					"description": "create: add several goals at once. Each becomes a goal; the first starts active if no goal is active, the rest queue.",
				},
				"goalId": map[string]any{
					"type":        "string",
					"description": "cancel/reorder: the queued goal's ID or friendly name to act on.",
				},
				"direction": map[string]any{
					"type":        "string",
					"enum":        []string{"up", "down"},
					"description": "reorder: move the queued goal up or down one position.",
				},
				"completionCriterion": map[string]any{
					"type":        "string",
					"description": "create: how to verify the goal is complete.",
				},
				"replace": map[string]any{
					"type":        "boolean",
					"description": "create: replace an existing goal instead of failing.",
				},
				"freshContext": map[string]any{
					"type":        "boolean",
					"description": "create: run this goal's continuation turns on a new agent with a clean context (objective + handoff only) instead of reusing the current conversation. History is preserved in the transcript but not sent to the new agent. Default false = reuse current context.",
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
				"todoTitle": map[string]any{
					"type":        "string",
					"description": "add_todo: title of the task to add to the goal's todo list.",
				},
				"todoId": map[string]any{
					"type":        "string",
					"description": "update_todo: ID of the todo item to update.",
				},
				"todoStatus": map[string]any{
					"type":        "string",
					"enum":        []string{"pending", "in_progress", "done"},
					"description": "update_todo: the new status for the todo item.",
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
	case "add_todo":
		return t.handleAddTodo(args)
	case "update_todo":
		return t.handleUpdateTodo(args)
	case "list":
		return t.handleList()
	case "cancel":
		return t.handleCancel(args)
	case "reorder":
		return t.handleReorder(args)
	default:
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_action",
			fmt.Errorf("invalid goal action %q: must be create, update, get, set_budget, add_todo, update_todo, list, cancel, or reorder", args.Action))
	}
}

func (t *GoalTool) handleAddTodo(args goalArgs) (agentic.ToolResult, error) {
	if args.TodoTitle == "" {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("action \"add_todo\" requires a non-empty \"todoTitle\""))
	}
	item, err := t.Mode.AddGoalTodo(args.TodoTitle, goal.GoalActorModel)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "add_todo_failed", err)
	}
	out, _ := json.Marshal(map[string]any{"todo": item})
	return agentic.ToolResult{Output: string(out)}, nil
}

func (t *GoalTool) handleUpdateTodo(args goalArgs) (agentic.ToolResult, error) {
	if args.TodoID == "" || args.TodoStatus == "" {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("action \"update_todo\" requires \"todoId\" and \"todoStatus\" (pending|in_progress|done)"))
	}
	snapshot, err := t.Mode.UpdateGoalTodo(args.TodoID, args.TodoStatus, goal.GoalActorModel)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "update_todo_failed", err)
	}
	out, _ := json.Marshal(map[string]any{"goal": goal.ForModel(snapshot)})
	return agentic.ToolResult{Output: string(out)}, nil
}

func (t *GoalTool) handleCreate(args goalArgs) (agentic.ToolResult, error) {
	// Gather the objectives: single `objective` or batch `objectives`.
	objectives := make([]string, 0, 1+len(args.Objectives))
	if args.Objective != "" {
		objectives = append(objectives, args.Objective)
	}
	objectives = append(objectives, args.Objectives...)
	if len(objectives) == 0 {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("action \"create\" requires \"objective\" or a non-empty \"objectives\" array"))
	}
	// Execution-time gate (bugs.md S2): autonomous creation is blocked only
	// when the feature flag is off AND no goal is active. All actions are
	// allowed while a goal is running.
	if t.CreateAllowed != nil && !t.CreateAllowed() {
		return agentic.ToolResult{}, goalToolErr("goal", "create_disabled",
			fmt.Errorf("autonomous goal creation is disabled; the user can start one with /goal"))
	}

	// Single-objective with replace keeps the legacy replace semantics exactly.
	if len(objectives) == 1 && args.Replace {
		return t.createActive(args, objectives[0], true)
	}

	// Todo-like default: ADD to the goal list. The first objective becomes the
	// active goal only when none is active; every objective after that (and all
	// of them when one is already active) is appended to the queue — never an
	// implicit replace.
	var activated *goal.GoalSnapshot
	queued := 0
	for _, obj := range objectives {
		if activated == nil && t.Mode.GetActiveGoal() == nil {
			res, err := t.createActive(args, obj, false)
			if err != nil {
				return res, err
			}
			snap := t.Mode.GetActiveGoal()
			activated = snap
			continue
		}
		if err := t.enqueue(obj, args.FreshContext); err != nil {
			return agentic.ToolResult{}, goalToolErr("goal", "create_failed", err)
		}
		queued++
	}
	return t.createResult(activated, queued)
}

// createActive starts obj as the active goal. replace mirrors the legacy
// replace-on-create behaviour.
func (t *GoalTool) createActive(args goalArgs, objective string, replace bool) (agentic.ToolResult, error) {
	snapshot, err := t.Mode.CreateGoal(goal.CreateGoalInput{
		Objective:           objective,
		CompletionCriterion: args.CompletionCriterion,
		Replace:             replace,
		FreshContext:        args.FreshContext,
	}, goal.GoalActorModel)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "create_failed", err)
	}
	out, _ := json.Marshal(map[string]any{"goal": goal.ForModel(snapshot)})
	return agentic.ToolResult{Output: string(out)}, nil
}

// enqueue appends a goal to the durable queue. It requires a wired queue.
func (t *GoalTool) enqueue(objective string, freshContext bool) error {
	if t.Queue == nil {
		return fmt.Errorf("a goal is already active and no goal queue is available; use replace to start a new one")
	}
	_, err := t.Queue.AppendWithOptions(objective, freshContext)
	return err
}

// createResult builds the create result, reporting the active goal and how
// many objectives were queued.
func (t *GoalTool) createResult(activated *goal.GoalSnapshot, queued int) (agentic.ToolResult, error) {
	payload := map[string]any{"queued": queued}
	if activated != nil {
		payload["goal"] = goal.ForModel(*activated)
	}
	out, _ := json.Marshal(payload)
	return agentic.ToolResult{Output: string(out)}, nil
}

// handleList returns the active goal plus the queued goals as a todo-like list.
func (t *GoalTool) handleList() (agentic.ToolResult, error) {
	payload := map[string]any{}
	if g := t.Mode.GetGoal().Goal; g != nil {
		payload["active"] = goal.ForModel(*g)
	} else {
		payload["active"] = nil
	}
	queued := []goal.UpcomingGoal{}
	if t.Queue != nil {
		if q, err := t.Queue.Read(); err == nil {
			queued = q
		}
	}
	payload["queued"] = queued
	payload["count"] = len(queued)
	out, _ := json.Marshal(payload)
	return agentic.ToolResult{Output: string(out)}, nil
}

// handleCancel removes a queued goal by ID or friendly name.
func (t *GoalTool) handleCancel(args goalArgs) (agentic.ToolResult, error) {
	if args.GoalID == "" {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("action \"cancel\" requires \"goalId\" (queued goal ID or name)"))
	}
	if t.Queue == nil {
		return agentic.ToolResult{}, goalToolErr("goal", "cancel_failed", fmt.Errorf("no goal queue available"))
	}
	id, err := t.resolveQueuedID(args.GoalID)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "cancel_failed", err)
	}
	_, removed, err := t.Queue.Remove(id)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "cancel_failed", err)
	}
	out, _ := json.Marshal(map[string]any{"cancelled": removed})
	return agentic.ToolResult{Output: string(out)}, nil
}

// handleReorder moves a queued goal up or down one position.
func (t *GoalTool) handleReorder(args goalArgs) (agentic.ToolResult, error) {
	if args.GoalID == "" || (args.Direction != "up" && args.Direction != "down") {
		return agentic.ToolResult{}, goalToolErr("goal", "invalid_input",
			fmt.Errorf("action \"reorder\" requires \"goalId\" and \"direction\" (up|down)"))
	}
	if t.Queue == nil {
		return agentic.ToolResult{}, goalToolErr("goal", "reorder_failed", fmt.Errorf("no goal queue available"))
	}
	id, err := t.resolveQueuedID(args.GoalID)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "reorder_failed", err)
	}
	goals, err := t.Queue.Move(id, args.Direction)
	if err != nil {
		return agentic.ToolResult{}, goalToolErr("goal", "reorder_failed", err)
	}
	out, _ := json.Marshal(map[string]any{"queued": goals})
	return agentic.ToolResult{Output: string(out)}, nil
}

// resolveQueuedID maps a goalId that may be a queue ID or a friendly name to
// the queue ID, so the model can reference goals by either.
func (t *GoalTool) resolveQueuedID(idOrName string) (string, error) {
	goals, err := t.Queue.Read()
	if err != nil {
		return "", err
	}
	for _, g := range goals {
		if g.ID == idOrName || g.Name == idOrName {
			return g.ID, nil
		}
	}
	return "", fmt.Errorf("no queued goal with ID or name %q", idOrName)
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
