// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
	"testing"
)

func TestGoalMode_TodoLifecycle(t *testing.T) {
	st := &testStore{}
	mode := NewGoalMode(st, nil, nil, nil)
	if _, err := mode.CreateGoal(CreateGoalInput{Objective: "multi-step"}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	a, err := mode.AddGoalTodo("first task", GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	b, err := mode.AddGoalTodo("second task", GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if a.ID == b.ID {
		t.Errorf("todo IDs not unique: %q", a.ID)
	}
	if _, err := mode.UpdateGoalTodo(a.ID, "done", GoalActorModel); err != nil {
		t.Fatal(err)
	}
	snap := mode.GetGoal().Goal
	if len(snap.Todos) != 2 {
		t.Fatalf("todos = %d, want 2", len(snap.Todos))
	}
	if snap.Todos[0].Status != TodoDone || snap.Todos[1].Status != TodoPending {
		t.Errorf("statuses = %q/%q", snap.Todos[0].Status, snap.Todos[1].Status)
	}

	// Replay round-trip preserves todos.
	mode2 := NewGoalMode(st, nil, nil, nil)
	if err := mode2.Replay(); err != nil {
		t.Fatal(err)
	}
	g := mode2.GetGoal().Goal
	if g == nil || len(g.Todos) != 2 || g.Todos[0].Status != TodoDone {
		t.Errorf("after replay todos = %+v", g)
	}
}

func TestGoalMode_TodoRequiresGoal(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	if _, err := mode.AddGoalTodo("x", GoalActorModel); err == nil {
		t.Error("AddGoalTodo with no goal should fail")
	}
}

func TestGoalMode_UpdateTodoNotFound(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "x"}, GoalActorUser)
	if _, err := mode.UpdateGoalTodo("nope", "done", GoalActorModel); err == nil {
		t.Error("UpdateGoalTodo on unknown id should fail")
	}
}

func TestTodoSummaryLine(t *testing.T) {
	if got := todoSummaryLine(nil); got != "" {
		t.Errorf("empty list should render empty, got %q", got)
	}
	items := []GoalTodoItem{
		{ID: "t1", Title: "done one", Status: TodoDone},
		{ID: "t2", Title: "wip", Status: TodoInProgress},
		{ID: "t3", Title: "later", Status: TodoPending},
	}
	got := todoSummaryLine(items)
	if !strings.Contains(got, "1/3 done") || !strings.Contains(got, "[x] done one") || !strings.Contains(got, "[~] wip") || !strings.Contains(got, "[ ] later") {
		t.Errorf("summary = %q", got)
	}
}
