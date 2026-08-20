// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package todo

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core/goal"
)

func TestTodoListAddAndList(t *testing.T) {
	tool := &TodoListTool{}
	if _, err := tool.Execute(`{"action":"add","description":"fix bug"}`); err != nil {
		t.Fatalf("add: %v", err)
	}
	out, err := tool.Execute(`{"action":"list"}`)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	if !strings.Contains(out, "fix bug") {
		t.Errorf("list output missing bug: %q", out)
	}
}

func TestTodoListComplete(t *testing.T) {
	tool := &TodoListTool{}
	tool.Execute(`{"action":"add","description":"fix bug"}`)
	out, err := tool.Execute(`{"action":"complete","id":"todo-1"}`)
	if err != nil {
		t.Fatalf("complete: %v", err)
	}
	if !strings.Contains(out, "done") {
		t.Errorf("complete output missing done: %q", out)
	}
}

func TestTodoListMissingDescription(t *testing.T) {
	tool := &TodoListTool{}
	_, err := tool.Execute(`{"action":"add"}`)
	if err == nil {
		t.Fatal("expected error for missing description")
	}
}

func TestTodoListClear(t *testing.T) {
	tool := &TodoListTool{}
	tool.Execute(`{"action":"add","description":"a"}`)
	if _, err := tool.Execute(`{"action":"clear"}`); err != nil {
		t.Fatalf("clear: %v", err)
	}
	if len(tool.Items()) != 0 {
		t.Error("expected empty after clear")
	}
}

// TestTodoList_GoalLinkage pins the lifecycle semantics: when a goal
// starts the todo list is BLANK and linked to the goal; goal todos never
// escape — the session list is preserved underneath and resurfaces when the
// goal ends.
func TestTodoList_GoalLinkage(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	tool := &TodoListTool{Mode: mode}

	// Session items first.
	if _, err := tool.Execute(`{"action":"add","description":"session task"}`); err != nil {
		t.Fatal(err)
	}

	// Goal starts → list is blank and goal-linked; session items do not leak.
	if _, err := mode.CreateGoal(goal.CreateGoalInput{Objective: "goal work"}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	out, err := tool.Execute(`{"action":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "linked to goal") && !strings.Contains(out, "goal-linked") {
		t.Errorf("during a goal the list must be goal-linked: %q", out)
	}
	if strings.Contains(out, "session task") {
		t.Errorf("goal list must start blank: %q", out)
	}

	// Add during the goal → goes to the goal's own list.
	if _, err := tool.Execute(`{"action":"add","description":"goal task"}`); err != nil {
		t.Fatal(err)
	}
	snap := mode.GetActiveGoal()
	if snap == nil || len(snap.Todos) != 1 || snap.Todos[0].Title != "goal task" {
		t.Fatalf("goal todos = %+v", snap.Todos)
	}
	// Status updates route to the goal list too (goal ids).
	if _, err := tool.Execute(`{"action":"complete","id":"` + snap.Todos[0].ID + `"}`); err != nil {
		t.Fatalf("complete: %v", err)
	}
	if got := mode.GetActiveGoal().Todos[0].Status; got != goal.TodoDone {
		t.Errorf("goal todo status = %q, want done", got)
	}

	// remove/clear are refused while goal-linked (containment).
	if _, err := tool.Execute(`{"action":"remove","id":"` + snap.Todos[0].ID + `"}`); err == nil {
		t.Error("remove during a goal must error (todos are goal-contained)")
	}
	if _, err := tool.Execute(`{"action":"clear"}`); err == nil {
		t.Error("clear during a goal must error (todos are goal-contained)")
	}

	// Goal ends → session list resurfaces; goal todos do not escape.
	if _, err := mode.CancelGoal(goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	out, err = tool.Execute(`{"action":"list"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "session task") {
		t.Errorf("session list must resurface after the goal: %q", out)
	}
	if strings.Contains(out, "goal task") {
		t.Errorf("goal todos must not escape the goal: %q", out)
	}
}
