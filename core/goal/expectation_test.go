// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import "testing"

// TestExpectation_PersistedAndReplayed proves the blocked expectation
// round-trips through the event log: a rebuilt mode sees the same terminal
// reason + expectation, and a resume clears both.
func TestExpectation_PersistedAndReplayed(t *testing.T) {
	st := &testStore{}
	mode := NewGoalMode(st, nil, nil, nil)
	if _, err := mode.CreateGoal(CreateGoalInput{Objective: "deploy"}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	reason, expectation := "missing credentials", "user provides AWS_ACCESS_KEY_ID"
	blocked, err := mode.MarkBlocked(GoalReasonInput{Reason: &reason, Expectation: &expectation}, GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if blocked.TerminalExpectation == nil || *blocked.TerminalExpectation != expectation {
		t.Fatalf("snapshot expectation = %v", blocked.TerminalExpectation)
	}
	replayed := NewGoalMode(st, nil, nil, nil)
	if err := replayed.Replay(); err != nil {
		t.Fatal(err)
	}
	assertReplayedExpectation(t, replayed, reason, expectation)
	if _, err := replayed.ResumeGoal(GoalReasonInput{}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	g := replayed.GetGoal().Goal
	if g.TerminalReason != nil || g.TerminalExpectation != nil {
		t.Errorf("resume must clear terminal fields")
	}
}

func assertReplayedExpectation(t *testing.T, mode *GoalMode, reason, expectation string) {
	g := mode.GetGoal().Goal
	if g == nil {
		t.Fatal("replayed goal missing")
	}
	if g.Status != GoalBlocked {
		t.Errorf("replayed status = %q", g.Status)
	}
	if g.TerminalReason == nil || *g.TerminalReason != reason {
		t.Errorf("replayed reason = %v", g.TerminalReason)
	}
	if g.TerminalExpectation == nil || *g.TerminalExpectation != expectation {
		t.Errorf("replayed expectation = %v", g.TerminalExpectation)
	}
}

// TestExpectation_PauseCarriesExpectation covers the pause path: a model may
// attach an expectation to a pause (what it is waiting for), persisted the
// same way as blocked.
func TestExpectation_PauseCarriesExpectation(t *testing.T) {
	mode := NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(CreateGoalInput{Objective: "wait on review"}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	reason := "user asked to hold"
	expectation := "user says continue"
	snap, err := mode.PauseGoal(GoalReasonInput{Reason: &reason, Expectation: &expectation}, GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if snap.TerminalExpectation == nil || *snap.TerminalExpectation != expectation {
		t.Errorf("pause expectation = %v", snap.TerminalExpectation)
	}
}

// TestMarkComplete_HasNoExpectation confirms completion never carries an
// expectation: the field is meaningful only for paused/blocked.
func TestMarkComplete_HasNoExpectation(t *testing.T) {
	mode := NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(CreateGoalInput{Objective: "ship it"}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	reason := "all validated"
	snap, err := mode.MarkComplete(GoalReasonInput{Reason: &reason}, GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if snap.TerminalExpectation != nil {
		t.Errorf("completed goal must not carry an expectation, got %v", *snap.TerminalExpectation)
	}
	if snap.TerminalReason == nil || *snap.TerminalReason != reason {
		t.Errorf("completion reason = %v", snap.TerminalReason)
	}
}
