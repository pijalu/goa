// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"testing"
	"time"
)

type testStore struct {
	records []GoalEventRecord
}

func (s *testStore) Append(record GoalEventRecord) error {
	s.records = append(s.records, record)
	return nil
}

func (s *testStore) Replay() ([]GoalEventRecord, error) {
	return s.records, nil
}

type testTelemetry struct {
	events []string
}

func (t *testTelemetry) Track(name string, _ map[string]any) {
	t.events = append(t.events, name)
}

type testPublisher struct {
	snaps []GoalSnapshot
}

func (p *testPublisher) Publish(snap *GoalSnapshot, _ *GoalChange) {
	if snap != nil {
		p.snaps = append(p.snaps, *snap)
	}
}

func TestCreateGoal(t *testing.T) {
	st := &testStore{}
	mode := NewGoalMode(st, nil, nil, nil)
	snap, err := mode.CreateGoal(CreateGoalInput{Objective: "fix bugs"}, GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if snap.Objective != "fix bugs" {
		t.Errorf("objective = %q", snap.Objective)
	}
	if snap.Status != GoalActive {
		t.Errorf("status = %q", snap.Status)
	}
	if len(st.records) != 1 || st.records[0].Type != GoalEventCreate {
		t.Errorf("records = %v", st.records)
	}
}

func TestCreateGoal_AlreadyActive(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	_, err := mode.CreateGoal(CreateGoalInput{Objective: "second"}, GoalActorUser)
	if err == nil {
		t.Fatal("expected error for duplicate active goal")
	}
}

func TestCreateGoal_Replace(t *testing.T) {
	st := &testStore{}
	mode := NewGoalMode(st, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	_, err := mode.CreateGoal(CreateGoalInput{Objective: "second", Replace: true}, GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Objective != "second" {
		t.Errorf("objective = %q", mode.GetGoal().Goal.Objective)
	}
	if len(st.records) != 3 {
		t.Errorf("records = %d", len(st.records))
	}
}

func TestPauseResume(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	reason := "user paused"
	_, err := mode.PauseGoal(GoalReasonInput{Reason: &reason}, GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Status != GoalPaused {
		t.Errorf("status = %q", mode.GetGoal().Goal.Status)
	}
	_, err = mode.ResumeGoal(GoalReasonInput{Reason: &reason}, GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal.Status != GoalActive {
		t.Errorf("status = %q", mode.GetGoal().Goal.Status)
	}
}

func TestCancelGoal(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	_, err := mode.CancelGoal(GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal != nil {
		t.Error("goal should be nil")
	}
}

func TestPauseResumeNoGoal(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	reason := "x"
	// CORE-BUG-10: these used to panic; they now return an error.
	if _, err := mode.PauseGoal(GoalReasonInput{Reason: &reason}, GoalActorUser); err == nil {
		t.Error("expected error pausing without a goal")
	}
	if _, err := mode.ResumeGoal(GoalReasonInput{Reason: &reason}, GoalActorUser); err == nil {
		t.Error("expected error resuming without a goal")
	}
	if _, err := mode.CancelGoal(GoalActorUser); err == nil {
		t.Error("expected error cancelling without a goal")
	}
}

func TestMarkComplete(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	reason := "done"
	_, err := mode.MarkComplete(GoalReasonInput{Reason: &reason}, GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if mode.GetGoal().Goal != nil {
		t.Error("goal should be cleared after completion")
	}
}

func TestIncrementTurnAndBudget(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	limit := 2
	mode.SetBudgetLimits(GoalBudgetLimits{TurnBudget: &limit}, GoalActorUser)
	mode.IncrementTurn()
	mode.IncrementTurn()
	if !mode.GetGoal().Goal.Budget.OverBudget {
		t.Error("budget should be exceeded after 2 turns")
	}
}

func TestRecordTokenUsage(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	mode.RecordTokenUsage(100)
	if mode.GetGoal().Goal.TokensUsed != 100 {
		t.Errorf("tokens = %d", mode.GetGoal().Goal.TokensUsed)
	}
}

func TestNormalizeAfterReplay(t *testing.T) {
	st := &testStore{}
	mode := NewGoalMode(st, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)

	mode2 := NewGoalMode(st, nil, nil, nil)
	if err := mode2.Replay(); err != nil {
		t.Fatal(err)
	}
	mode2.NormalizeAfterReplay()
	if mode2.GetGoal().Goal.Status != GoalPaused {
		t.Errorf("status = %q", mode2.GetGoal().Goal.Status)
	}
}

func TestReplayCompletion(t *testing.T) {
	st := &testStore{}
	mode := NewGoalMode(st, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	reason := "done"
	mode.MarkComplete(GoalReasonInput{Reason: &reason}, GoalActorUser)

	mode2 := NewGoalMode(st, nil, nil, nil)
	if err := mode2.Replay(); err != nil {
		t.Fatal(err)
	}
	mode2.NormalizeAfterReplay()
	if mode2.GetGoal().Goal != nil {
		t.Error("completed goal should be cleared on replay")
	}
}

func TestReplayPausedGoal(t *testing.T) {
	st := &testStore{}
	mode := NewGoalMode(st, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	reason := "paused"
	mode.PauseGoal(GoalReasonInput{Reason: &reason}, GoalActorUser)

	mode2 := NewGoalMode(st, nil, nil, nil)
	if err := mode2.Replay(); err != nil {
		t.Fatal(err)
	}
	mode2.NormalizeAfterReplay()
	if mode2.GetGoal().Goal == nil || mode2.GetGoal().Goal.Status != GoalPaused {
		t.Errorf("status = %q", mode2.GetGoal().Goal.Status)
	}
}

func TestTelemetry(t *testing.T) {
	tel := &testTelemetry{}
	mode := NewGoalMode(&testStore{}, nil, tel, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	mode.IncrementTurn()
	if len(tel.events) < 2 {
		t.Errorf("events = %v", tel.events)
	}
}

func TestPublisher(t *testing.T) {
	pub := &testPublisher{}
	mode := NewGoalMode(&testStore{}, pub, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	if len(pub.snaps) != 1 {
		t.Errorf("snaps = %d", len(pub.snaps))
	}
}

func TestPauseActiveGoal(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	snap, err := mode.PauseActiveGoal(GoalReasonInput{Reason: strPtr("paused")}, GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if snap == nil || snap.Status != GoalPaused {
		t.Errorf("status = %q", snap.Status)
	}
}

func TestPauseActiveGoal_NoGoal(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	snap, err := mode.PauseActiveGoal(GoalReasonInput{Reason: strPtr("paused")}, GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if snap != nil {
		t.Error("expected nil")
	}
}

func TestPauseActiveGoal_AlreadyPaused(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	mode.PauseGoal(GoalReasonInput{Reason: strPtr("paused")}, GoalActorUser)
	snap, err := mode.PauseActiveGoal(GoalReasonInput{Reason: strPtr("again")}, GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if snap != nil {
		t.Error("expected nil when already paused")
	}
}

func TestMarkBlocked(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	snap, err := mode.MarkBlocked(GoalReasonInput{Reason: strPtr("blocker")}, GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if snap == nil || snap.Status != GoalBlocked {
		t.Errorf("status = %q", snap.Status)
	}
}

func TestMarkBlocked_NoGoal(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	snap, err := mode.MarkBlocked(GoalReasonInput{Reason: strPtr("blocker")}, GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if snap != nil {
		t.Error("expected nil")
	}
}

func TestGetActiveGoal(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	if mode.GetActiveGoal() != nil {
		t.Error("expected nil")
	}
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	if mode.GetActiveGoal() == nil {
		t.Error("expected active goal")
	}
}

func TestPauseOnInterrupt(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	snap, err := mode.PauseOnInterrupt("stopped")
	if err != nil {
		t.Fatal(err)
	}
	if snap == nil || snap.Status != GoalPaused {
		t.Errorf("status = %q", snap.Status)
	}
}

func TestRecordTokenUsage_Negative(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	mode.RecordTokenUsage(-10)
	if mode.GetGoal().Goal.TokensUsed != 0 {
		t.Errorf("tokens = %d", mode.GetGoal().Goal.TokensUsed)
	}
}

func TestIncrementTurn_NoGoal(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)
	snap, err := mode.IncrementTurn()
	if err != nil {
		t.Fatal(err)
	}
	if snap != nil {
		t.Error("expected nil")
	}
}

func TestNormalizeCompletionCriterion(t *testing.T) {
	empty := ""
	if got := normalizeCompletionCriterion(&empty); got != nil {
		t.Error("expected nil for empty")
	}
	spaced := "  done  "
	if got := normalizeCompletionCriterion(&spaced); *got != "done" {
		t.Errorf("got %q", *got)
	}
}

func TestLiveWallClockMs(t *testing.T) {
	now := time.Now()
	resume := now.UnixMilli()
	state := goalStage{
		status:             GoalActive,
		wallClockMs:        1000,
		wallClockResumedAt: &resume,
	}
	got := LiveWallClockMs(state, now.Add(2*time.Second))
	if got < 2000 {
		t.Errorf("wall clock = %d", got)
	}
}

var _ EventStore = (*testStore)(nil)
var _ Telemetry = (*testTelemetry)(nil)
var _ EventPublisher = (*testPublisher)(nil)

// TestRequireStateNoPanic guards against CORE-BUG-10: PauseGoal/ResumeGoal/
// CancelGoal/SetBudgetLimits previously called requireState which panicked
// with "no current goal" when no goal was active. These are reachable from
// user commands, so they must return an error instead of crashing the process.
func TestRequireStateNoPanic(t *testing.T) {
	mode := NewGoalMode(&testStore{}, nil, nil, nil)

	reason := "testing"
	cases := []struct {
		name string
		call func() error
	}{
		{"PauseGoal", func() error {
			_, err := mode.PauseGoal(GoalReasonInput{Reason: &reason}, GoalActorUser)
			return err
		}},
		{"ResumeGoal", func() error {
			_, err := mode.ResumeGoal(GoalReasonInput{Reason: &reason}, GoalActorUser)
			return err
		}},
		{"CancelGoal", func() error {
			_, err := mode.CancelGoal(GoalActorUser)
			return err
		}},
		{"SetBudgetLimits", func() error {
			tb := 1000
			_, err := mode.SetBudgetLimits(GoalBudgetLimits{TokenBudget: &tb}, GoalActorUser)
			return err
		}},
	}
	for _, tc := range cases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			// A panic here would fail the test via the runtime panic.
			err := tc.call()
			if err == nil {
				t.Fatalf("expected error when no goal is active")
			}
		})
	}
}
