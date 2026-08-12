// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import (
	"strings"
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

// clearCapturePublisher records the change carried by each goal update so a
// test can inspect what the clear event (nil snapshot) published.
type clearCapturePublisher struct {
	snaps   []*GoalSnapshot
	changes []*GoalChange
}

func (p *clearCapturePublisher) Publish(snap *GoalSnapshot, change *GoalChange) {
	p.snaps = append(p.snaps, snap)
	p.changes = append(p.changes, change)
}

// A cancel must mark its clear event as GoalChangeClear with the cancelling
// actor: consumers use it to park the queued successor PAUSED instead of
// auto-starting it. A completion clear carries NO change (drains the queue).
func TestCancelGoal_ClearEventCarriesActor(t *testing.T) {
	pub := &clearCapturePublisher{}
	mode := NewGoalMode(&testStore{}, pub, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	if _, err := mode.CancelGoal(GoalActorUser); err != nil {
		t.Fatal(err)
	}

	var clearChange *GoalChange
	for i, snap := range pub.snaps {
		if snap == nil {
			clearChange = pub.changes[i]
		}
	}
	if clearChange == nil {
		t.Fatal("cancel clear event carried no change")
	}
	if clearChange.Kind != GoalChangeClear {
		t.Errorf("clear change kind = %q, want %q", clearChange.Kind, GoalChangeClear)
	}
	if clearChange.Actor == nil || *clearChange.Actor != GoalActorUser {
		t.Errorf("clear change actor = %+v, want user", clearChange.Actor)
	}
}

// The completion clear keeps the legacy contract: no change on the clear
// event, so hosts auto-start the queued successor (queue drains on complete).
func TestMarkComplete_ClearEventCarriesNoChange(t *testing.T) {
	pub := &clearCapturePublisher{}
	mode := NewGoalMode(&testStore{}, pub, nil, nil)
	mode.CreateGoal(CreateGoalInput{Objective: "first"}, GoalActorUser)
	reason := "done"
	if _, err := mode.MarkComplete(GoalReasonInput{Reason: &reason}, GoalActorUser); err != nil {
		t.Fatal(err)
	}

	var sawClear bool
	for i, snap := range pub.snaps {
		if snap == nil {
			sawClear = true
			if pub.changes[i] != nil {
				t.Errorf("completion clear event must carry no change, got %+v", pub.changes[i])
			}
		}
	}
	if !sawClear {
		t.Fatal("no clear event published after completion")
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

// TestCreateGoal_FreshContext verifies the per-goal clean-context flag is
// captured on create, exposed on the snapshot, persisted to the event log, and
// restored on replay (per-goal clean-context flag).
func TestCreateGoal_FreshContext(t *testing.T) {
	st := &testStore{}
	mode := NewGoalMode(st, nil, nil, nil)
	snap, err := mode.CreateGoal(CreateGoalInput{Objective: "self-contained task", FreshContext: true}, GoalActorModel)
	if err != nil {
		t.Fatal(err)
	}
	if !snap.FreshContext {
		t.Error("snapshot.FreshContext = false, want true")
	}
	// Default (flag unset) is false: reuse the current agent.
	snap2, err := mode.CreateGoal(CreateGoalInput{Objective: "normal", Replace: true}, GoalActorUser)
	if err != nil {
		t.Fatal(err)
	}
	if snap2.FreshContext {
		t.Error("default FreshContext = true, want false (reuse current agent)")
	}

	// Replay round-trip preserves the flag.
	st2 := &testStore{}
	m1 := NewGoalMode(st2, nil, nil, nil)
	if _, err := m1.CreateGoal(CreateGoalInput{Objective: "fresh", FreshContext: true}, GoalActorModel); err != nil {
		t.Fatal(err)
	}
	m2 := NewGoalMode(st2, nil, nil, nil)
	if err := m2.Replay(); err != nil {
		t.Fatal(err)
	}
	if g := m2.GetGoal().Goal; g == nil || !g.FreshContext {
		t.Errorf("after replay FreshContext = %v, want true", g)
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

// TestValidateObjective covers the creation-entry length gate: boundary
// values, the markdown-pointer hint, and the rune-vs-byte measurement —
// a pasted TUI transcript (2275 runes but 4491 BYTES of box-drawing) must
// be measured in characters, matching the error message's wording.
func TestValidateObjective(t *testing.T) {
	boxObjective := strings.Repeat("─", 2275) // 2275 runes, 6825 bytes
	if got := len(boxObjective); got <= MaxObjectiveLength {
		t.Fatalf("test setup: box objective must exceed the byte cap (%d bytes)", got)
	}

	cases := []struct {
		name      string
		objective string
		wantErr   bool
	}{
		{"empty passes (length-only validator)", "", false},
		{"short objective", "fix the bug", false},
		{"exactly at limit", strings.Repeat("a", MaxObjectiveLength), false},
		{"one over limit", strings.Repeat("a", MaxObjectiveLength+1), true},
		{"multibyte runes counted as characters", boxObjective, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateObjective(tc.objective)
			if tc.wantErr && err == nil {
				t.Fatal("expected rejection, got nil")
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("unexpected rejection: %v", err)
			}
			if tc.wantErr {
				if !strings.Contains(err.Error(), "markdown") {
					t.Errorf("rejection must hint at the markdown-document workaround: %v", err)
				}
				if !strings.Contains(err.Error(), "4000") {
					t.Errorf("rejection must state the limit: %v", err)
				}
			}
		})
	}
}

// TestCreateGoalAcceptsOversizedObjective is the no-dead-end guarantee:
// GoalMode.CreateGoal is the shared internal path for resume, queue
// promotion, and auto-unblock — it must NEVER reject a stored objective
// for length, or a goal that predates/bypasses entry validation becomes
// unstartable (the /goal:resume "objective cannot exceed 4000" zombie).
func TestCreateGoalAcceptsOversizedObjective(t *testing.T) {
	st := &testStore{}
	mode := NewGoalMode(st, nil, nil, nil)
	big := strings.Repeat("x", MaxObjectiveLength+1000)
	snap, err := mode.CreateGoal(CreateGoalInput{Objective: big}, GoalActorUser)
	if err != nil {
		t.Fatalf("CreateGoal must not length-reject a stored objective: %v", err)
	}
	if snap.Objective != big {
		t.Errorf("objective len = %d, want %d", len(snap.Objective), len(big))
	}
}
