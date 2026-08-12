// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package todo

import (
	"embed"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tools/common"
)

// TodoListTool manages an in-session todo list.
//
// Goal linkage (bugs.md): when a goal is ACTIVE the tool operates on the
// goal's linked todo list instead of the session list — the goal starts with
// a blank list, items added during it are contained by it (the stall
// watchdog fingerprints their transitions and the completion reminder
// surfaces any left open), and they die with the goal. The session list is
// preserved underneath and resurfaces when the goal ends.
type TodoListTool struct {
	agentic.BaseTool
	// Mode links the tool to the active goal's todo list. Nil = session list only.
	Mode *goal.GoalMode

	mu     sync.RWMutex
	items  []TodoItem
	nextID int // monotonically increasing, never reused
}

// TodoItem represents a single todo entry.
type TodoItem struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

const (
	statusPending    = "pending"
	statusInProgress = "in_progress"
	statusDone       = "done"
)

type todoInput struct {
	Action      string `json:"action"`
	ID          string `json:"id"`
	Description string `json:"description"`
	Status      string `json:"status"`
}

// Schema returns the tool schema.
func (t *TodoListTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "todo_list",
		Description: "Track tasks with a todo list.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"action": map[string]any{
					"type": "string",
					"enum":        []string{"add", "update", "complete", "remove", "list", "clear"},
				},
				"id": map[string]any{
					"type":        "string",
					"description": "todo ID for update/complete/remove",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "todo description for add",
				},
				"status": map[string]any{
					"type": "string",
					"enum":        []string{"pending", "in_progress", "done"},
				},
			},
			"required": []string{"action"},
		},
	}
}

// Execute runs the todo action.
func (t *TodoListTool) Execute(input string) (string, error) {
	var p todoInput
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", &internal.ToolError{
			Tool: "todo_list", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Provide valid JSON with an action field.",
		}
	}
	switch p.Action {
	case "add":
		return t.add(p.Description)
	case "update":
		return t.update(p.ID, p.Status)
	case "complete":
		return t.update(p.ID, statusDone)
	case "remove":
		return t.remove(p.ID)
	case "list":
		return t.list()
	case "clear":
		return t.clear()
	default:
		return "", &internal.ToolError{Tool: "todo_list", Type: "invalid_action", Detail: fmt.Sprintf("unknown action %q", p.Action), HintText: "Use add, update, complete, remove, list, or clear."}
	}
}

// goalActive reports whether a goal is currently active — the todo list is
// then linked to that goal (blank at goal start, contained by it).
func (t *TodoListTool) goalActive() bool {
	return t.Mode != nil && t.Mode.GetActiveGoal() != nil
}

// addGoalTodo routes an add to the goal's linked list.
func (t *TodoListTool) addGoalTodo(desc string) (string, error) {
	item, err := t.Mode.AddGoalTodo(strings.TrimSpace(desc), goal.GoalActorModel)
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("[todo_list] Added %s: %s (linked to goal)", item.ID, item.Title), nil
}

// updateGoalTodo routes a status change to the goal's linked list.
func (t *TodoListTool) updateGoalTodo(id, status string) (string, error) {
	if status == "" {
		status = statusInProgress
	}
	if _, err := t.Mode.UpdateGoalTodo(id, status, goal.GoalActorModel); err != nil {
		return "", err
	}
	return fmt.Sprintf("[todo_list] Updated %s (%s) (linked to goal)", id, status), nil
}

// listGoalTodos renders the goal's linked list (empty at goal start).
func (t *TodoListTool) listGoalTodos() (string, error) {
	snap := t.Mode.GetActiveGoal()
	if snap == nil || len(snap.Todos) == 0 {
		return "[todo_list] No todos (goal-linked list is blank)", nil
	}
	var b strings.Builder
	fmt.Fprintln(&b, "[todo_list] Todos (linked to goal):")
	for _, item := range snap.Todos {
		fmt.Fprintf(&b, "  [%s] %s - %s\n", item.Status, item.ID, item.Title)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (t *TodoListTool) add(desc string) (string, error) {
	if strings.TrimSpace(desc) == "" {
		return "", &internal.ToolError{Tool: "todo_list", Type: "missing_description", Detail: "description is required for add", HintText: "Provide a description."}
	}
	if t.goalActive() {
		return t.addGoalTodo(desc)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.nextID++
	id := fmt.Sprintf("todo-%d", t.nextID)
	t.items = append(t.items, TodoItem{ID: id, Description: desc, Status: statusPending})
	return fmt.Sprintf("[todo_list] Added %s: %s", id, desc), nil
}

func (t *TodoListTool) update(id, status string) (string, error) {
	if id == "" {
		return "", &internal.ToolError{Tool: "todo_list", Type: "missing_id", Detail: "id is required", HintText: "Provide the todo id."}
	}
	if t.goalActive() {
		return t.updateGoalTodo(id, status)
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for i := range t.items {
		if t.items[i].ID == id {
			if status != "" {
				t.items[i].Status = status
			}
			return fmt.Sprintf("[todo_list] Updated %s (%s)", id, t.items[i].Status), nil
		}
	}
	return "", &internal.ToolError{Tool: "todo_list", Type: "not_found", Detail: fmt.Sprintf("todo %q not found", id), HintText: "Use list to see available ids."}
}

func (t *TodoListTool) remove(id string) (string, error) {
	if id == "" {
		return "", &internal.ToolError{Tool: "todo_list", Type: "missing_id", Detail: "id is required", HintText: "Provide the todo id."}
	}
	if t.goalActive() {
		return "", &internal.ToolError{Tool: "todo_list", Type: "goal_linked",
			Detail:   "todos are linked to the active goal and cannot be removed individually",
			HintText: "Mark the item done instead, or let it close with the goal."}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	for i, item := range t.items {
		if item.ID == id {
			t.items = append(t.items[:i], t.items[i+1:]...)
			return fmt.Sprintf("[todo_list] Removed %s", id), nil
		}
	}
	return "", &internal.ToolError{Tool: "todo_list", Type: "not_found", Detail: fmt.Sprintf("todo %q not found", id), HintText: "Use list to see available ids."}
}

func (t *TodoListTool) list() (string, error) {
	if t.goalActive() {
		return t.listGoalTodos()
	}
	t.mu.RLock()
	items := make([]TodoItem, len(t.items))
	copy(items, t.items)
	t.mu.RUnlock()
	if len(items) == 0 {
		return "[todo_list] No todos", nil
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ID < items[j].ID })
	var b strings.Builder
	fmt.Fprintln(&b, "[todo_list] Todos:")
	for _, item := range items {
		fmt.Fprintf(&b, "  [%s] %s - %s\n", item.Status, item.ID, item.Description)
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (t *TodoListTool) clear() (string, error) {
	if t.goalActive() {
		return "", &internal.ToolError{Tool: "todo_list", Type: "goal_linked",
			Detail:   "todos are linked to the active goal and cannot be cleared",
			HintText: "Goal todos close with the goal; mark items done instead."}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.items = nil
	return "[todo_list] Cleared all todos", nil
}

// Items returns a copy of the current todo items.
func (t *TodoListTool) Items() []TodoItem {
	t.mu.RLock()
	defer t.mu.RUnlock()
	out := make([]TodoItem, len(t.items))
	copy(out, t.items)
	return out
}

// IsRetryable returns false.
func (t *TodoListTool) IsRetryable(err error) bool { return false }

// ShortDoc returns a short doc string.
//
//go:embed todo.short.md todo.long.md
var todoDocs embed.FS

func (t *TodoListTool) ShortDoc() string { return common.ReadDoc(todoDocs, "todo.short.md") }
func (t *TodoListTool) LongDoc() string  { return common.ReadDoc(todoDocs, "todo.long.md") }
func (t *TodoListTool) Examples() []string {
	return []string{
		`{"action": "add", "description": "Fix login bug"}`,
		`{"action": "complete", "id": "todo-1"}`,
		`{"action": "list"}`,
	}
}
