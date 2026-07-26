// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
	"testing"
)

func TestParseDoneGate(t *testing.T) {
	cases := []struct {
		in   string
		want DoneGateMode
		ok   bool
	}{
		{"", DoneGateVerify, true},
		{"verify", DoneGateVerify, true},
		{"VERIFY", DoneGateVerify, true},
		{" evidence ", DoneGateEvidence, true},
		{"off", DoneGateOff, true},
		{"strict", "", false},
		{"on", "", false},
	}
	for _, tc := range cases {
		got, ok := ParseDoneGate(tc.in)
		if ok != tc.ok || got != tc.want {
			t.Errorf("ParseDoneGate(%q) = (%q, %v), want (%q, %v)", tc.in, got, ok, tc.want, tc.ok)
		}
	}
}

// gatedMode creates a goal with a completion criterion and the given gate.
func gatedMode(t *testing.T, gate DoneGateMode) *GoalMode {
	t.Helper()
	m := NewGoalMode(nil, nil, nil, nil)
	m.SetDoneGate(gate)
	criterion := "go test ./... passes"
	if _, err := m.CreateGoal(CreateGoalInput{
		Objective:           "make the build green",
		CompletionCriterion: &criterion,
	}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestRequestComplete_VerifyChallengesThenCloses(t *testing.T) {
	m := gatedMode(t, DoneGateVerify)

	// First model complete: challenged, goal stays active.
	snap, challenged, err := m.RequestComplete(GoalReasonInput{}, GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if !challenged {
		t.Fatal("first complete must be challenged in verify mode")
	}
	if snap == nil || snap.Status != GoalActive {
		t.Fatalf("goal must stay active after challenge, got %+v", snap)
	}
	if got := m.GetActiveGoal(); got == nil {
		t.Fatal("goal was cleared despite pending verification")
	}

	// Second complete without evidence: rejected.
	if _, _, err := m.RequestComplete(GoalReasonInput{}, GoalActorModel); err == nil {
		t.Fatal("second complete without reason must fail")
	}

	// Second complete with evidence: closes.
	evidence := "ran go test ./...: all packages ok"
	completed, challenged, err := m.RequestComplete(GoalReasonInput{Reason: &evidence}, GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if challenged {
		t.Fatal("confirmed complete must not be challenged again")
	}
	if completed == nil || completed.Status != GoalDone {
		t.Fatalf("expected completed snapshot, got %+v", completed)
	}
	if completed.TerminalReason == nil || *completed.TerminalReason != evidence {
		t.Errorf("evidence must be recorded as terminal reason, got %v", completed.TerminalReason)
	}
	if m.GetGoal().Goal != nil {
		t.Error("goal record must be cleared after completion")
	}
}

func TestRequestComplete_VerifyChallengeContent(t *testing.T) {
	m := gatedMode(t, DoneGateVerify)
	snap, challenged, err := m.RequestComplete(GoalReasonInput{}, GoalActorModel)
	if err != nil || !challenged {
		t.Fatalf("expected challenge, got challenged=%v err=%v", challenged, err)
	}
	challenge := BuildVerificationChallenge(*snap)
	if !strings.Contains(challenge, "go test ./... passes") {
		t.Error("challenge must restate the completion criterion")
	}
	if !strings.Contains(challenge, "reason") {
		t.Error("challenge must instruct the model to re-call complete with reason")
	}
}

func TestRequestComplete_EvidenceRequiresReasonSingleCall(t *testing.T) {
	m := gatedMode(t, DoneGateEvidence)
	if _, _, err := m.RequestComplete(GoalReasonInput{}, GoalActorModel); err == nil {
		t.Fatal("evidence mode must reject a reason-less complete")
	}
	if m.GetActiveGoal() == nil {
		t.Fatal("goal must stay active after rejected complete")
	}
	evidence := "all tests pass"
	completed, challenged, err := m.RequestComplete(GoalReasonInput{Reason: &evidence}, GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if challenged {
		t.Fatal("evidence mode must not challenge")
	}
	if completed == nil {
		t.Fatal("expected completion")
	}
}

func TestRequestComplete_OffClosesImmediately(t *testing.T) {
	m := gatedMode(t, DoneGateOff)
	completed, challenged, err := m.RequestComplete(GoalReasonInput{}, GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if challenged || completed == nil {
		t.Fatalf("gate off must close immediately, got challenged=%v completed=%v", challenged, completed)
	}
}

func TestRequestComplete_SkippedActorsAndStates(t *testing.T) {
	// User-initiated completion bypasses the gate even with a criterion.
	m := gatedMode(t, DoneGateVerify)
	completed, challenged, err := m.RequestComplete(GoalReasonInput{}, GoalActorUser)
	if err != nil || challenged || completed == nil {
		t.Fatalf("user completion must bypass gate: challenged=%v completed=%v err=%v", challenged, completed, err)
	}

	// No criterion recorded: nothing to verify, closes immediately.
	m2 := NewGoalMode(nil, nil, nil, nil)
	m2.SetDoneGate(DoneGateVerify)
	if _, err := m2.CreateGoal(CreateGoalInput{Objective: "no criterion"}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	completed, challenged, err = m2.RequestComplete(GoalReasonInput{}, GoalActorModel)
	if err != nil || challenged || completed == nil {
		t.Fatalf("criterion-less goal must bypass gate: challenged=%v completed=%v err=%v", challenged, completed, err)
	}

	// Orchestrator-managed goal: bypasses gate (the orchestrator evaluates).
	m3 := NewGoalMode(nil, nil, nil, nil)
	m3.SetDoneGate(DoneGateVerify)
	criterion := "stage done"
	if _, err := m3.CreateGoal(CreateGoalInput{Objective: "stage", ManagedBy: "orchestrator", CompletionCriterion: &criterion}, GoalActorUser); err != nil {
		t.Fatal(err)
	}
	completed, challenged, err = m3.RequestComplete(GoalReasonInput{}, GoalActorModel)
	if err != nil || challenged || completed == nil {
		t.Fatalf("managed goal must bypass gate: challenged=%v completed=%v err=%v", challenged, completed, err)
	}

	// No active goal: nil, no error (MarkComplete semantics).
	m4 := NewGoalMode(nil, nil, nil, nil)
	completed, challenged, err = m4.RequestComplete(GoalReasonInput{}, GoalActorModel)
	if err != nil || challenged || completed != nil {
		t.Fatalf("no goal: want (nil,false,nil), got (%v,%v,%v)", completed, challenged, err)
	}
}

func TestRequestComplete_PauseAfterChallengeReArmsGate(t *testing.T) {
	m := gatedMode(t, DoneGateVerify)
	if _, challenged, _ := m.RequestComplete(GoalReasonInput{}, GoalActorModel); !challenged {
		t.Fatal("expected challenge")
	}
	reason := "need to think"
	if _, err := m.PauseGoal(GoalReasonInput{Reason: &reason}, GoalActorModel); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResumeGoal(GoalReasonInput{}, GoalActorModel); err != nil {
		t.Fatal(err)
	}
	// The pause must have cleared pendingVerification: completing again
	// re-challenges instead of closing.
	_, challenged, err := m.RequestComplete(GoalReasonInput{}, GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if !challenged {
		t.Fatal("pause/resume cycle must re-arm the verification challenge")
	}
}

func TestRequestComplete_BlockedAfterChallengeReArmsGate(t *testing.T) {
	m := gatedMode(t, DoneGateVerify)
	if _, challenged, _ := m.RequestComplete(GoalReasonInput{}, GoalActorModel); !challenged {
		t.Fatal("expected challenge")
	}
	reason := "missing API key"
	expectation := "user provides OPENAI_API_KEY"
	if _, err := m.MarkBlocked(GoalReasonInput{Reason: &reason, Expectation: &expectation}, GoalActorModel); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ResumeGoal(GoalReasonInput{}, GoalActorModel); err != nil {
		t.Fatal(err)
	}
	if _, challenged, _ := m.RequestComplete(GoalReasonInput{}, GoalActorModel); !challenged {
		t.Fatal("blocked/resume cycle must re-arm the verification challenge")
	}
}
