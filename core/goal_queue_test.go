// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"os"
	"path/filepath"
	"strings"
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

func TestGoalQueueStore_Clear(t *testing.T) {
	store := NewGoalQueueStore(filepath.Join(t.TempDir(), "q.json"))
	store.Append("A")
	store.Append("B")

	cleared, err := store.Clear()
	if err != nil {
		t.Fatal(err)
	}
	if len(cleared) != 2 || cleared[0].Objective != "A" || cleared[1].Objective != "B" {
		t.Errorf("cleared = %+v, want [A B] in queue order", cleared)
	}
	read, err := store.Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 0 {
		t.Errorf("queue should be empty after Clear, got %+v", read)
	}

	// Clearing an already-empty queue is a no-op, not an error.
	cleared, err = store.Clear()
	if err != nil || len(cleared) != 0 {
		t.Errorf("second Clear = %+v, %v; want empty, nil", cleared, err)
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

// TestGoalQueueStore_CompletionCriterion verifies a queued goal keeps its
// done-condition through Append/persist/Read so promotion can restore it.
func TestGoalQueueStore_CompletionCriterion(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.json")
	store := NewGoalQueueStore(path)
	criterion := "go test ./... passes"
	verifyCmd := "go test ./..."
	blank := "   "
	if _, err := store.AppendGoal(goal.UpcomingGoalInput{Objective: "with criterion", CompletionCriterion: &criterion, VerifyCommand: &verifyCmd}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendGoal(goal.UpcomingGoalInput{Objective: "blank criterion", CompletionCriterion: &blank, VerifyCommand: &blank}); err != nil {
		t.Fatal(err)
	}
	read, err := NewGoalQueueStore(path).Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 2 {
		t.Fatalf("read = %d goals, want 2", len(read))
	}
	if read[0].CompletionCriterion == nil || *read[0].CompletionCriterion != criterion {
		t.Errorf("goal[0] criterion = %v, want %q", read[0].CompletionCriterion, criterion)
	}
	if read[0].VerifyCommand == nil || *read[0].VerifyCommand != verifyCmd {
		t.Errorf("goal[0] verify command = %v, want %q", read[0].VerifyCommand, verifyCmd)
	}
	if read[1].CompletionCriterion != nil {
		t.Errorf("blank criterion must normalize to nil, got %q", *read[1].CompletionCriterion)
	}
	if read[1].VerifyCommand != nil {
		t.Errorf("blank verify command must normalize to nil, got %q", *read[1].VerifyCommand)
	}
}

// TestGoalQueueStore_FreshContext verifies a queued goal carries its per-goal
// clean-context flag through Append/persist/Read (bugs.md: goal queue +
// per-goal clean-context flag).
func TestGoalQueueStore_FreshContext(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.json")
	store := NewGoalQueueStore(path)
	if _, err := store.AppendGoal(goal.UpcomingGoalInput{Objective: "default goal"}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendGoal(goal.UpcomingGoalInput{Objective: "clean goal", FreshContext: true}); err != nil {
		t.Fatal(err)
	}
	// Re-read from disk to prove persistence.
	read, err := NewGoalQueueStore(path).Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 2 {
		t.Fatalf("read = %d goals, want 2", len(read))
	}
	if read[0].FreshContext {
		t.Error("goal[0] FreshContext = true, want false (default)")
	}
	if !read[1].FreshContext {
		t.Error("goal[1] FreshContext = false, want true")
	}
}

// TestGoalQueueStore_Handover verifies a queued goal carries its handover
// continuity note through Append/persist/Read (durable across restart) and
// enforces the cap at enqueue time.
func TestGoalQueueStore_Handover(t *testing.T) {
	path := filepath.Join(t.TempDir(), "q.json")
	store := NewGoalQueueStore(path)
	handover := "State: done. Next: verify."
	blank := "   "
	if _, err := store.AppendGoal(goal.UpcomingGoalInput{Objective: "with handover", Handoff: &handover}); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AppendGoal(goal.UpcomingGoalInput{Objective: "blank handover", Handoff: &blank}); err != nil {
		t.Fatal(err)
	}
	// Re-read from disk to prove durability (survives restart).
	read, err := NewGoalQueueStore(path).Read()
	if err != nil {
		t.Fatal(err)
	}
	if len(read) != 2 {
		t.Fatalf("read = %d goals, want 2", len(read))
	}
	if read[0].Handoff == nil || *read[0].Handoff != handover {
		t.Errorf("goal[0] handover = %v, want %q", read[0].Handoff, handover)
	}
	if read[1].Handoff != nil {
		t.Errorf("blank handover must normalize to nil, got %q", *read[1].Handoff)
	}
	// The persisted file uses the handover surface key.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), `"handover": "State: done. Next: verify."`) {
		t.Errorf("queue file must persist the handover key: %s", data)
	}
	// Over-long handover is rejected at enqueue.
	tooLong := strings.Repeat("z", goal.MaxHandoverLength+1)
	if _, err := store.AppendGoal(goal.UpcomingGoalInput{Objective: "too long", Handoff: &tooLong}); err == nil {
		t.Error("expected cap error for over-long handover")
	}
}
