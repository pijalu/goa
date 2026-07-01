// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/core/goal"
)

func TestGoalQueueStore_AppendRead(t *testing.T) {
	store := NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	goals, err := store.Append("first")
	if err != nil {
		t.Fatal(err)
	}
	if len(goals) != 1 || goals[0].Objective != "first" {
		t.Errorf("goals = %v", goals)
	}
	read, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 1 {
		t.Errorf("read = %d", len(read))
	}
}

func TestGoalQueueStore_Update(t *testing.T) {
	store := NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	goals, _ := store.Append("first")
	updated, err := store.Update(goals[0].ID, "renamed")
	if err != nil {
		t.Fatal(err)
	}
	if updated[0].Objective != "renamed" {
		t.Errorf("objective = %q", updated[0].Objective)
	}
}

func TestGoalQueueStore_UpdateNotFound(t *testing.T) {
	store := NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	_, err := store.Update("missing", "x")
	if err == nil {
		t.Error("expected error")
	}
}

func TestGoalQueueStore_Remove(t *testing.T) {
	store := NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	goals, _ := store.Append("first")
	remaining, removed, err := store.Remove(goals[0].ID)
	if err != nil {
		t.Fatal(err)
	}
	if removed == nil || removed.Objective != "first" {
		t.Errorf("removed = %v", removed)
	}
	if len(remaining) != 0 {
		t.Errorf("remaining = %d", len(remaining))
	}
}

func TestGoalQueueStore_RemoveNotFound(t *testing.T) {
	store := NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	_, _, err := store.Remove("missing")
	if err == nil {
		t.Error("expected error")
	}
}

// TestGoalQueueStore_RemoveAliasing guards against CORE-BUG-1: a previous
// implementation reused the source backing array (goals[:0]) for the filtered
// slice and returned a pointer into it, so the returned *removed pointed at
// whichever element shifted into the removed slot during compaction.
func TestGoalQueueStore_RemoveAliasing(t *testing.T) {
	store := NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	store.Append("A")
	store.Append("B")
	store.Append("C")

	all, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	targetID := all[1].ID // objective "B"
	remaining, removed, err := store.Remove(targetID)
	if err != nil {
		t.Fatal(err)
	}
	if removed == nil {
		t.Fatal("removed is nil")
	}
	if removed.ID == "" || removed.Objective != "B" {
		t.Errorf("removed = %+v, want objective B", removed)
	}
	if len(remaining) != 2 {
		t.Fatalf("remaining = %d, want 2", len(remaining))
	}
	// Mutating the returned slice must not corrupt the captured removed goal.
	for i := range remaining {
		remaining[i].Objective = "MUTATED"
	}
	if removed.Objective != "B" {
		t.Errorf("removed.Objective mutated to %q via filtered aliasing", removed.Objective)
	}
}

func TestGoalQueueStore_MoveUpDown(t *testing.T) {
	store := NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	store.Append("a")
	store.Append("b")
	store.Append("c")

	goals, _ := store.Read()
	moved, err := store.Move(goals[1].ID, "up")
	if err != nil {
		t.Fatal(err)
	}
	if moved[0].Objective != "b" || moved[1].Objective != "a" {
		t.Errorf("up move failed: %v", moved)
	}

	goals, _ = store.Read()
	moved, err = store.Move(goals[1].ID, "down")
	if err != nil {
		t.Fatal(err)
	}
	if moved[1].Objective != "c" || moved[2].Objective != "a" {
		t.Errorf("down move failed: %v", moved)
	}
}

func TestGoalQueueStore_MoveInvalid(t *testing.T) {
	store := NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	goals, _ := store.Append("a")
	_, err := store.Move(goals[0].ID, "left")
	if err == nil {
		t.Error("expected error for invalid direction")
	}
}

func TestGoalQueueStore_Restore(t *testing.T) {
	store := NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	store.Append("a")
	store.Restore(goal.UpcomingGoal{ID: "r", Objective: "restored"})
	goals, _ := store.Read()
	if len(goals) != 2 || goals[0].Objective != "restored" {
		t.Errorf("goals = %v", goals)
	}
}

func TestGoalQueueStore_ReorderByMappingErrors(t *testing.T) {
	store := NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	store.Append("a")
	store.Append("b")

	cases := []string{"bad", "1Z", "1A,1A", "1A,2B,3C"}
	for _, c := range cases {
		_, err := store.ReorderByMapping(c)
		if err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestGoalQueueStore_ReorderEmptyMapping(t *testing.T) {
	store := NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	store.Append("a")
	goals, err := store.ReorderByMapping("")
	if err != nil {
		t.Fatal(err)
	}
	if len(goals) != 1 {
		t.Errorf("goals = %d", len(goals))
	}
}
