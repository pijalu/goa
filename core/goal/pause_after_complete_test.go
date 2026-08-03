// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import "testing"

// TestSetPauseAfterComplete pins the /goal:pause:next one-shot flag: it is
// off by default, arms and disarms on demand, and each transition is visible
// on the snapshot consumers receive.
func TestSetPauseAfterComplete(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	if _, err := mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if g := mode.GetGoal().Goal; g.PauseAfterComplete {
		t.Fatal("PauseAfterComplete = true on a new goal, want false")
	}

	snap, err := mode.SetPauseAfterComplete(true, GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.PauseAfterComplete {
		t.Error("snapshot after arm: PauseAfterComplete = false, want true")
	}
	if g := mode.GetGoal().Goal; !g.PauseAfterComplete {
		t.Error("goal after arm: PauseAfterComplete = false, want true")
	}

	snap, err = mode.SetPauseAfterComplete(false, GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if snap.PauseAfterComplete {
		t.Error("snapshot after disarm: PauseAfterComplete = true, want false")
	}
	if g := mode.GetGoal().Goal; g.PauseAfterComplete {
		t.Error("goal after disarm: PauseAfterComplete = true, want false")
	}
}

// TestSetPauseAfterComplete_CompletionEventCarriesFlag closes the mode→app
// loop: a real MarkComplete on an armed goal publishes the completion event
// with a snapshot whose PauseAfterComplete is true — the value the app's
// promotion policy stashes to park the successor paused.
func TestSetPauseAfterComplete_CompletionEventCarriesFlag(t *testing.T) {
	pub := &testPublisher{}
	mode := NewGoalMode(&testStore{}, pub, nil, nil)
	if _, err := mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := mode.SetPauseAfterComplete(true, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := mode.MarkComplete(GoalReasonInput{}, GoalActorModel); err != nil {
		t.Fatal(err)
	}

	// The completion snapshot is the last published (the clear emits nil).
	if len(pub.snaps) == 0 {
		t.Fatal("no snapshots published")
	}
	done := pub.snaps[len(pub.snaps)-1]
	if done.Status != GoalDone {
		t.Fatalf("last snapshot status = %q, want complete", done.Status)
	}
	if !done.PauseAfterComplete {
		t.Error("completion snapshot PauseAfterComplete = false, want true (armed)")
	}
}

// SetPauseAfterComplete without a goal must report an error, not panic —
// the /goal:pause:next command is reachable at any time (CORE-BUG-10 class).
func TestSetPauseAfterComplete_NoGoal(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	if _, err := mode.SetPauseAfterComplete(true, GoalActorUser); err == nil {
		t.Fatal("SetPauseAfterComplete with no goal: expected error, got nil")
	}
}

// The flag is durable: it is patched into the event log so a session restart
// (replay) keeps the armed (or disarmed) state.
func TestSetPauseAfterComplete_Replay(t *testing.T) {
	st := &testStore{}
	m1 := NewGoalMode(st, nil, nil, nil)
	if _, err := m1.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	if _, err := m1.SetPauseAfterComplete(true, GoalActorUser); err != nil {
		t.Fatal(err)
	}

	m2 := NewGoalMode(st, nil, nil, nil)
	if err := m2.Replay(); err != nil {
		t.Fatal(err)
	}
	if g := m2.GetGoal().Goal; g == nil || !g.PauseAfterComplete {
		t.Fatalf("after replay PauseAfterComplete = %+v, want armed", g)
	}

	// A disarm patch also survives replay.
	if _, err := m1.SetPauseAfterComplete(false, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	m3 := NewGoalMode(st, nil, nil, nil)
	if err := m3.Replay(); err != nil {
		t.Fatal(err)
	}
	if g := m3.GetGoal().Goal; g == nil || g.PauseAfterComplete {
		t.Fatalf("after disarm replay PauseAfterComplete = %+v, want disarmed", g)
	}
}
