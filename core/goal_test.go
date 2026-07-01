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

func TestGoalManager_CreateAndGet(t *testing.T) {
	gm := NewGoalManager(t.TempDir())

	goal, err := gm.CreateGoal("Refactor the auth module")
	if err != nil {
		t.Fatal(err)
	}
	if goal.Status != GoalActive {
		t.Errorf("new goal status = %q, want %q", goal.Status, GoalActive)
	}

	loaded := gm.ActiveGoal()
	if loaded == nil {
		t.Fatal("active goal should not be nil")
	}
	if loaded.Objective != "Refactor the auth module" {
		t.Errorf("objective = %q, want %q", loaded.Objective, "Refactor the auth module")
	}
}

func TestGoalManager_EmptyObjective_Error(t *testing.T) {
	gm := NewGoalManager(t.TempDir())
	_, err := gm.CreateGoal("")
	if err == nil {
		t.Error("expected error for empty objective")
	}
}

func TestGoalManager_PauseResume(t *testing.T) {
	gm := NewGoalManager(t.TempDir())
	g, _ := gm.CreateGoal("test")

	if err := gm.PauseGoal(g.ID); err != nil {
		t.Fatal(err)
	}
	if gm.ActiveGoal() != nil {
		t.Error("active goal should be nil after pause")
	}

	if err := gm.ResumeGoal(g.ID); err != nil {
		t.Fatal(err)
	}
	loaded := gm.ActiveGoal()
	if loaded == nil || loaded.Status != GoalActive {
		t.Errorf("after resume: status = %q, want %q", loaded.Status, GoalActive)
	}
}

func TestGoalManager_CancelRemoves(t *testing.T) {
	gm := NewGoalManager(t.TempDir())
	g, _ := gm.CreateGoal("test")

	if err := gm.CancelGoal(g.ID); err != nil {
		t.Fatal(err)
	}
	if gm.GetGoal(g.ID) != nil {
		t.Error("cancelled goal should not exist")
	}
	err := gm.CancelGoal("nonexistent")
	if err == nil {
		t.Error("expected error for cancel non-existent")
	}
}

func TestGoalManager_Complete_Clears(t *testing.T) {
	gm := NewGoalManager(t.TempDir())
	g, _ := gm.CreateGoal("test")

	if err := gm.CompleteGoal(g.ID); err != nil {
		t.Fatal(err)
	}
	if gm.GetGoal(g.ID) != nil {
		t.Error("completed goal should be cleared")
	}
}

func TestGoalManager_ResumeAlreadyActive_NoOp(t *testing.T) {
	gm := NewGoalManager(t.TempDir())
	g, _ := gm.CreateGoal("test")

	if err := gm.ResumeGoal(g.ID); err != nil {
		t.Fatal(err) // should be no-op, not error
	}
}

func TestGoalManager_NewGoalPausesOld(t *testing.T) {
	gm := NewGoalManager(t.TempDir())
	first, _ := gm.CreateGoal("first")
	second, _ := gm.CreateGoal("second")

	active := gm.ActiveGoal()
	if active == nil || active.ID != second.ID {
		t.Error("active goal should be the second one")
	}
	firstLoaded := gm.GetGoal(first.ID)
	if firstLoaded == nil || firstLoaded.Status != GoalPaused {
		t.Errorf("first goal should be paused, got %q", firstLoaded.Status)
	}
}

func TestGoalManager_CreateGoal_UniqueIDs(t *testing.T) {
	gm := NewGoalManager(t.TempDir())
	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		goal, err := gm.CreateGoal(strings.Repeat("g", i+1))
		if err != nil {
			t.Fatalf("CreateGoal: %v", err)
		}
		if seen[goal.ID] {
			t.Fatalf("duplicate goal ID %q", goal.ID)
		}
		seen[goal.ID] = true
	}
}

func TestGoalManager_Persistence(t *testing.T) {
	dir := t.TempDir()

	gm1 := NewGoalManager(dir)
	gm1.CreateGoal("Refactor auth")
	gm1.Save()

	gm2 := NewGoalManager(dir)
	goals := gm2.ListGoals()
	var found bool
	for _, g := range goals {
		if g.Objective == "Refactor auth" {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("expected goal after reload, got %d goals", len(goals))
	}
}

func TestGoalManager_ActiveGoalPrompt(t *testing.T) {
	gm := NewGoalManager(t.TempDir())
	if prompt := gm.ActiveGoalPrompt(); prompt != "" {
		t.Errorf("expected empty prompt with no goals, got %q", prompt)
	}

	gm.CreateGoal("test task")
	prompt := gm.ActiveGoalPrompt()
	if prompt == "" {
		t.Fatal("expected non-empty prompt with active goal")
	}
	if !strings.Contains(prompt, "test task") {
		t.Errorf("prompt should contain objective: %s", prompt)
	}
}

func TestGoalManager_Save_CreatesDir(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "deep", "nested")
	gm := NewGoalManager(dir)
	gm.CreateGoal("test")
	if err := gm.Save(); err != nil {
		t.Fatal(err)
	}
	entries, _ := os.ReadDir(filepath.Join(dir, "goals"))
	if len(entries) == 0 {
		t.Fatal("goal file not created")
	}
}

func TestGoalManager_Load_NonExistentDir(t *testing.T) {
	gm := NewGoalManager("/nonexistent/path")
	err := gm.Load()
	if err != nil {
		t.Fatal(err) // should not error on missing dir
	}
}

func TestGoalManager_ListEmpty(t *testing.T) {
	gm := NewGoalManager(t.TempDir())
	goals := gm.ListGoals()
	if len(goals) != 0 {
		t.Errorf("expected 0 goals, got %d", len(goals))
	}
}

func TestGoalManager_ObjectiveTooLong(t *testing.T) {
	gm := NewGoalManager(t.TempDir())
	long := strings.Repeat("a", 4001)
	_, err := gm.CreateGoal(long)
	if err == nil {
		t.Error("expected error for too-long objective")
	}
}

func TestGoalManager_GoalModeDelegation(t *testing.T) {
	gm := NewGoalManager(t.TempDir())
	g, _ := gm.CreateGoal("delegated")

	modeGoal := gm.Mode.GetGoal().Goal
	if modeGoal == nil || modeGoal.Objective != "delegated" {
		t.Error("GoalManager should delegate to GoalMode")
	}

	_ = g
}

func TestGoalManager_QueueAppend(t *testing.T) {
	gm := NewGoalManager(t.TempDir())
	goals, err := gm.Queue.Append("next task")
	if err != nil {
		t.Fatal(err)
	}
	if len(goals) != 1 || goals[0].Objective != "next task" {
		t.Errorf("expected 1 queued goal, got %+v", goals)
	}
}

func TestGoalManager_QueueReorder(t *testing.T) {
	gm := NewGoalManager(t.TempDir())
	gm.Queue.Append("first")
	gm.Queue.Append("second")
	gm.Queue.Append("third")

	goals, err := gm.Queue.ReorderByMapping("1C,2A,3B")
	if err != nil {
		t.Fatal(err)
	}
	if len(goals) != 3 {
		t.Fatalf("expected 3 goals, got %d", len(goals))
	}
	if goals[0].Objective != "third" || goals[1].Objective != "first" || goals[2].Objective != "second" {
		t.Errorf("reorder failed: %v", goals)
	}
}

var _ = goal.GoalActive

// TestNewGoalManager_SurfacesReplayError guards against CORE-BUG-5: a previous
// implementation did `_ = mode.Replay()` in NewGoalManager, silently swallowing
// a corrupt/unreadable event log and erasing the user's active goal. The error
// must now be surfaced via Restore/ReplayError rather than silently usable.
func TestNewGoalManager_SurfacesReplayError(t *testing.T) {
	mode := goal.NewGoalMode(&errReplayStore{}, nil, nil, nil)
	gm := NewGoalManagerWithMode(t.TempDir(), mode)
	if err := gm.Restore(); err == nil {
		t.Fatalf("Restore: expected replay error, got nil")
	}
	if err := gm.ReplayError(); err == nil {
		t.Errorf("ReplayError: expected captured error, got nil")
	}
}

// errReplayStore is an EventStore whose Replay always fails.
type errReplayStore struct{}

func (errReplayStore) Append(goal.GoalEventRecord) error { return nil }
func (errReplayStore) Replay() ([]goal.GoalEventRecord, error) {
	return nil, &replayErr{msg: "simulated disk failure"}
}

type replayErr struct{ msg string }

func (e *replayErr) Error() string { return e.msg }

var _ goal.EventStore = (*errReplayStore)(nil)
