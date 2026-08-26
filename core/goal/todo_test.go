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
	assertTodoMutation(t, mode)
	assertTodoReplayRoundTrip(t, st)
}

// assertTodoMutation adds two todos, marks one done, and checks the live
// snapshot reflects the lifecycle.
func assertTodoMutation(t *testing.T, mode *GoalMode) {
	t.Helper()
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
}

// assertTodoReplayRoundTrip replays the store into a fresh GoalMode and
// verifies todos survive.
func assertTodoReplayRoundTrip(t *testing.T, st EventStore) {
	t.Helper()
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

// TestTodoMutationsPublishSnapshots (Issue 4): todo add/update must
// publish a goal snapshot so the footer's ⬩ pending-todo markers refresh
// live — a silent persist left the status line stale until the next goal
// lifecycle event.
func TestTodoMutationsPublishSnapshots(t *testing.T) {
	st := &testStore{}
	pub := &testPublisher{}
	mode := NewGoalMode(st, pub, nil, nil)
	if _, err := mode.CreateGoal(CreateGoalInput{Objective: "x"}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	pub.snaps = nil // ignore the create publish

	if _, err := mode.AddGoalTodo("task one", GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if len(pub.snaps) == 0 {
		t.Fatal("AddGoalTodo published no snapshot")
	}
	last := pub.snaps[len(pub.snaps)-1]
	if len(last.Todos) != 1 || last.Todos[0].Title != "task one" {
		t.Fatalf("published snapshot after add = %+v", last)
	}

	if _, err := mode.UpdateGoalTodo("t1", TodoDone, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	last = pub.snaps[len(pub.snaps)-1]
	if len(last.Todos) != 1 || last.Todos[0].Status != TodoDone {
		t.Fatalf("published snapshot after update = %+v", last)
	}
}

// TestRenameGoalTodo renames an existing todo and persists it (replay-safe),
// publishing a snapshot like every other todo mutation (Issue 5).
func TestRenameGoalTodo(t *testing.T) {
	st := &testStore{}
	pub := &testPublisher{}
	mode := NewGoalMode(st, pub, nil, nil)
	if _, err := mode.CreateGoal(CreateGoalInput{Objective: "x"}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := mode.AddGoalTodo("old title", GoalActorUser); err != nil {
		t.Fatal(err)
	}

	if _, err := mode.RenameGoalTodo("t1", "new title", GoalActorUser); err != nil {
		t.Fatalf("RenameGoalTodo: %v", err)
	}
	g := mode.GetGoal().Goal
	if g.Todos[0].Title != "new title" {
		t.Errorf("title = %q, want %q", g.Todos[0].Title, "new title")
	}
	if len(pub.snaps) == 0 {
		t.Error("rename published no snapshot")
	}

	// Replay round-trip preserves the rename.
	mode2 := NewGoalMode(st, nil, nil, nil)
	if err := mode2.Replay(); err != nil {
		t.Fatal(err)
	}
	if g2 := mode2.GetGoal().Goal; g2 == nil || g2.Todos[0].Title != "new title" {
		t.Errorf("after replay title = %+v", g2)
	}

	// Errors: unknown id, empty title, no goal.
	if _, err := mode.RenameGoalTodo("t9", "x", GoalActorUser); err == nil {
		t.Error("unknown id: want error")
	}
	if _, err := mode.RenameGoalTodo("t1", "  ", GoalActorUser); err == nil {
		t.Error("empty title: want error")
	}
}

// TestRemoveGoalTodo deletes a todo, keeps IDs of the survivors stable, and
// persists the removal through replay (Issue 5).
func TestRemoveGoalTodo(t *testing.T) {
	st := &testStore{}
	pub := &testPublisher{}
	mode := NewGoalMode(st, pub, nil, nil)
	if _, err := mode.CreateGoal(CreateGoalInput{Objective: "x"}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	mode.AddGoalTodo("one", GoalActorUser)
	mode.AddGoalTodo("two", GoalActorUser)
	mode.AddGoalTodo("three", GoalActorUser)

	if _, err := mode.RemoveGoalTodo("t2", GoalActorUser); err != nil {
		t.Fatalf("RemoveGoalTodo: %v", err)
	}
	g := mode.GetGoal().Goal
	if len(g.Todos) != 2 || g.Todos[0].ID != "t1" || g.Todos[1].ID != "t3" {
		t.Errorf("todos after remove = %+v", g.Todos)
	}
	if len(pub.snaps) == 0 {
		t.Error("remove published no snapshot")
	}

	mode2 := NewGoalMode(st, nil, nil, nil)
	if err := mode2.Replay(); err != nil {
		t.Fatal(err)
	}
	if g2 := mode2.GetGoal().Goal; g2 == nil || len(g2.Todos) != 2 {
		t.Errorf("after replay todos = %+v", g2.Todos)
	}

	if _, err := mode.RemoveGoalTodo("t2", GoalActorUser); err == nil {
		t.Error("removing a deleted id: want error")
	}
}
