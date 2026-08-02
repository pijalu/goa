// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

// promoteRecordingRunner is a core.AgentRunner that records the objective of
// the goal each continuation turn ran for, then completes the goal so the
// drive loop exits after one turn.
type promoteRecordingRunner struct {
	mu         sync.Mutex
	objectives []string
	mode       *goal.GoalMode
}

func (r *promoteRecordingRunner) Run(_ context.Context, _ string) error {
	objective := ""
	if active := r.mode.GetActiveGoal(); active != nil {
		objective = active.Objective
	}
	r.mu.Lock()
	r.objectives = append(r.objectives, objective)
	r.mu.Unlock()
	_, _ = r.mode.MarkComplete(goal.GoalReasonInput{}, goal.GoalActorRuntime)
	return nil
}

func (r *promoteRecordingRunner) runs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.objectives...)
}

// Regression test: an auto-promoted queued goal must actually start driving.
// promoteNextQueuedGoal runs on the event-forwarder goroutine after the clear
// event crossed the async bus, so the post-turn hook and the previous drive
// loop have both already observed "no active goal" and stood down. Without a
// driver kick at the promotion site the promoted goal idled active with
// 0 turns until the user typed something.
func TestHandleGoalUpdate_ClearPromotesQueuedGoalAndStartsDriver(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mgr := core.NewGoalManagerWithMode("", mode)
	mgr.Queue = core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	if _, err := mgr.Queue.AppendGoal(goal.UpcomingGoalInput{Objective: "queued objective"}); err != nil {
		t.Fatalf("append queued goal: %v", err)
	}
	runner := &promoteRecordingRunner{mode: mode}
	driver := &core.GoalDriver{Mode: mode, Agent: runner}

	a := &App{}
	a.subs = &subsystems{
		chat:        tui.NewChatViewport(),
		goalManager: mgr,
		goalDriver:  driver,
	}

	// The active goal cleared (completion/cancel): the queued goal promotes.
	a.handleGoalUpdate(&event.GoalUpdate{Snapshot: nil})

	if q, err := mgr.Queue.Read(); err != nil || len(q) != 0 {
		t.Fatalf("queue after promotion = %v, %v; want empty", q, err)
	}

	deadline := time.Now().Add(2 * time.Second)
	for {
		runs := runner.runs()
		if len(runs) > 0 {
			if runs[0] != "queued objective" {
				t.Fatalf("driver ran turn for objective %q, want %q", runs[0], "queued objective")
			}
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("promoted goal never received a continuation turn: driver was not started")
		}
		time.Sleep(5 * time.Millisecond)
	}
}

// TestHandleGoalUpdate_CompletionStashesTerminalReason verifies the
// completion→auto-promotion handover path (spec section 2, existing behavior):
// a GoalChangeCompletion event stashes the completed goal's TerminalReason so
// the next auto-promoted queued goal inherits it as its handover.
func TestHandleGoalUpdate_CompletionStashesTerminalReason(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mgr := core.NewGoalManagerWithMode("", mode)
	reason := "completed: all tests pass (evidence: go test exit 0)"
	snap := &goal.GoalSnapshot{Objective: "done", Status: goal.GoalDone, TerminalReason: &reason}

	a := &App{}
	a.subs = &subsystems{
		chat:        tui.NewChatViewport(),
		goalManager: mgr,
	}

	a.handleGoalUpdate(&event.GoalUpdate{
		Snapshot: snap,
		Change:   &goal.GoalChange{Kind: goal.GoalChangeCompletion},
	})
	if a.goalCompletionHandoff == nil || *a.goalCompletionHandoff != reason {
		t.Fatalf("completion evidence not stashed for auto-promotion: %+v", a.goalCompletionHandoff)
	}
}

// TestPromoteNextQueuedGoal_InheritsTerminalReason verifies an auto-promoted
// queued goal (with no stored handover) inherits the predecessor's
// TerminalReason as its handover — the completion-evidence behavior the spec
// must preserve.
func TestPromoteNextQueuedGoal_InheritsTerminalReason(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mgr := core.NewGoalManagerWithMode("", mode)
	mgr.Queue = core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	if _, err := mgr.Queue.AppendGoal(goal.UpcomingGoalInput{Objective: "queued objective"}); err != nil {
		t.Fatalf("append queued goal: %v", err)
	}

	a := &App{}
	a.subs = &subsystems{
		chat:        tui.NewChatViewport(),
		goalManager: mgr,
	}
	reason := "completed: all tests pass (evidence: go test exit 0)"
	a.goalCompletionHandoff = &reason

	a.promoteNextQueuedGoal()

	g := mode.GetGoal().Goal
	if g == nil {
		t.Fatal("promoted goal not active")
	}
	if g.Handoff == nil || *g.Handoff != reason {
		t.Fatalf("promoted goal must inherit predecessor TerminalReason as handover, got %+v", g.Handoff)
	}
}

// TestPromoteNextQueuedGoal_StoredHandoverWins verifies the explicit-caller
// rule: a queued goal with its own stored handover keeps it on auto-promotion
// instead of overwriting it with the predecessor's terminal evidence.
func TestPromoteNextQueuedGoal_StoredHandoverWins(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mgr := core.NewGoalManagerWithMode("", mode)
	mgr.Queue = core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	explicit := "explicit handover from creator"
	if _, err := mgr.Queue.AppendGoal(goal.UpcomingGoalInput{Objective: "queued objective", Handoff: &explicit}); err != nil {
		t.Fatalf("append queued goal: %v", err)
	}

	a := &App{}
	a.subs = &subsystems{
		chat:        tui.NewChatViewport(),
		goalManager: mgr,
	}
	reason := "predecessor completion evidence"
	a.goalCompletionHandoff = &reason

	a.promoteNextQueuedGoal()

	g := mode.GetGoal().Goal
	if g == nil {
		t.Fatal("promoted goal not active")
	}
	if g.Handoff == nil || *g.Handoff != explicit {
		t.Fatalf("stored handover must win over predecessor evidence, got %+v", g.Handoff)
	}
}

// clearChange builds a goal-cleared change with the cancelling actor, as
// CancelGoal emits on the clear event.
func clearChange(actor goal.GoalActor) *goal.GoalChange {
	return &goal.GoalChange{Kind: goal.GoalChangeClear, Actor: &actor}
}

// TestCancelClearPausesPromotion pins the successor policy: only explicit
// user/model cancels park the next goal; completion clears (no change) and
// runtime framework clears (postpone, unblock, orchestrator) keep driving.
func TestCancelClearPausesPromotion(t *testing.T) {
	user := goal.GoalActorUser
	cases := []struct {
		name   string
		change *goal.GoalChange
		want   bool
	}{
		{"completion clear (no change)", nil, false},
		{"user cancel pauses", clearChange(goal.GoalActorUser), true},
		{"model cancel pauses", clearChange(goal.GoalActorModel), true},
		{"runtime cancel keeps driving", clearChange(goal.GoalActorRuntime), false},
		{"system cancel keeps driving", clearChange(goal.GoalActorSystem), false},
		{"lifecycle change is not a clear", &goal.GoalChange{Kind: goal.GoalChangeLifecycle, Actor: &user}, false},
	}
	for _, tc := range cases {
		if got := cancelClearPausesPromotion(tc.change); got != tc.want {
			t.Errorf("%s: cancelClearPausesPromotion = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// newCancelPolicyTestApp builds the minimal app wiring shared by the
// clear-policy tests: a queue with one queued goal, a recording runner, and
// the drive machinery the promotion touches.
func newCancelPolicyTestApp(t *testing.T) (*App, *goal.GoalMode, *promoteRecordingRunner) {
	t.Helper()
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mgr := core.NewGoalManagerWithMode("", mode)
	mgr.Queue = core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json"))
	if _, err := mgr.Queue.AppendGoal(goal.UpcomingGoalInput{Objective: "queued objective"}); err != nil {
		t.Fatalf("append queued goal: %v", err)
	}
	runner := &promoteRecordingRunner{mode: mode}
	a := &App{}
	a.subs = &subsystems{
		chat:        tui.NewChatViewport(),
		goalManager: mgr,
		goalDriver:  &core.GoalDriver{Mode: mode, Agent: runner},
	}
	return a, mode, runner
}

// A USER cancel must NOT start the next goal: the queue head is promoted but
// parked PAUSED, and no continuation turn launches.
func TestHandleGoalUpdate_UserCancelClearPromotesPaused(t *testing.T) {
	a, mode, runner := newCancelPolicyTestApp(t)

	a.handleGoalUpdate(&event.GoalUpdate{Snapshot: nil, Change: clearChange(goal.GoalActorUser)})

	g := mode.GetGoal().Goal
	if g == nil {
		t.Fatal("queue head should be promoted after a cancel")
	}
	if g.Status != goal.GoalPaused {
		t.Fatalf("promoted goal status = %q, want paused", g.Status)
	}
	if g.Objective != "queued objective" {
		t.Fatalf("promoted objective = %q, want queued objective", g.Objective)
	}
	if runs := runner.runs(); len(runs) != 0 {
		t.Fatalf("cancel launched %d continuation turns, want 0", len(runs))
	}
}

// A MODEL cancel (goal tool) likewise parks the successor paused.
func TestHandleGoalUpdate_ModelCancelClearPromotesPaused(t *testing.T) {
	a, mode, runner := newCancelPolicyTestApp(t)

	a.handleGoalUpdate(&event.GoalUpdate{Snapshot: nil, Change: clearChange(goal.GoalActorModel)})

	g := mode.GetGoal().Goal
	if g == nil || g.Status != goal.GoalPaused {
		t.Fatalf("promoted goal = %+v, want paused", g)
	}
	if runs := runner.runs(); len(runs) != 0 {
		t.Fatalf("model cancel launched %d continuation turns, want 0", len(runs))
	}
}

// A RUNTIME clear (postpone, auto-unblock, orchestrator cleanup) is framework
// scheduling machinery: the queue head promotes ACTIVE and drives on, exactly
// as after a completion.
func TestHandleGoalUpdate_RuntimeCancelClearStartsNext(t *testing.T) {
	a, mode, runner := newCancelPolicyTestApp(t)

	a.handleGoalUpdate(&event.GoalUpdate{Snapshot: nil, Change: clearChange(goal.GoalActorRuntime)})

	g := mode.GetGoal().Goal
	if g == nil || g.Status != goal.GoalActive {
		t.Fatalf("promoted goal = %+v, want active", g)
	}
	deadline := time.Now().Add(2 * time.Second)
	for len(runner.runs()) == 0 {
		if time.Now().After(deadline) {
			t.Fatal("runtime clear did not start the promoted goal")
		}
		time.Sleep(5 * time.Millisecond)
	}
}
