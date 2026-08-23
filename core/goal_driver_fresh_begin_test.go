// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"context"
	"testing"

	"github.com/pijalu/goa/core/goal"
)

// busyThenFreshAgent simulates the race where a fresh-context goal's FIRST
// continuation turn is attempted while another turn owns the agent:
// RunFresh returns ErrAgentBusy without performing any context reset.
// Later attempts run with the agent idle and succeed, completing the goal
// after completeOnCall fresh turns.
type busyThenFreshAgent struct {
	mode           *goal.GoalMode
	completeOnCall int    // 1-based RunFresh call that completes the goal
	sawBeginSeq    []bool // begin flag observed per RunFresh call
}

func (a *busyThenFreshAgent) Run(ctx context.Context, prompt string) error {
	_, _ = a.mode.MarkComplete(goal.GoalReasonInput{}, goal.GoalActorModel)
	return nil
}

func (a *busyThenFreshAgent) RunFresh(ctx context.Context, prompt string, begin bool) error {
	call := len(a.sawBeginSeq) + 1
	a.sawBeginSeq = append(a.sawBeginSeq, begin)
	if call < a.completeOnCall {
		// First attempt only: the agent is busy — the reset never ran.
		if call == 1 {
			return ErrAgentBusy
		}
		return nil
	}
	_, _ = a.mode.MarkComplete(goal.GoalReasonInput{}, goal.GoalActorModel)
	return nil
}

// TestGoalDriver_FreshBeginSurvivesBusyAttempt pins the begin-flag contract:
// a failed first attempt (ErrAgentBusy performs NO reset inside RunFresh)
// must NOT consume the begin marker. The next drive attempt must retry with
// begin=true so the context reset actually happens; otherwise the goal runs
// its whole life on the pre-goal conversation — exactly the leak the
// fresh-context flag exists to prevent.
func TestGoalDriver_FreshBeginSurvivesBusyAttempt(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{
		Objective:    "self-contained",
		FreshContext: true,
	}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	fake := &busyThenFreshAgent{mode: mode, completeOnCall: 2}
	driver := &GoalDriver{Agent: fake, Mode: mode}

	// Attempt 1: busy — clean stop, goal stays active, nothing consumed.
	if err := driver.Drive(context.Background()); err != nil {
		t.Fatalf("Drive #1 returned %v, want nil (busy is a clean stop)", err)
	}
	if got := len(fake.sawBeginSeq); got != 1 || !fake.sawBeginSeq[0] {
		t.Fatalf("after Drive #1 sawBeginSeq = %v, want [true]", fake.sawBeginSeq)
	}
	if g := mode.GetGoal().Goal; g == nil || g.Status != goal.GoalActive {
		t.Fatalf("goal not active after busy attempt: %+v", mode.GetGoal().Goal)
	}

	// Attempt 2: agent idle again — the reset MUST be retried (begin=true).
	if err := driver.Drive(context.Background()); err != nil {
		t.Fatalf("Drive #2 returned %v", err)
	}
	want := []bool{true, true}
	if len(fake.sawBeginSeq) != len(want) {
		t.Fatalf("sawBeginSeq = %v, want %v", fake.sawBeginSeq, want)
	}
	for i, w := range want {
		if fake.sawBeginSeq[i] != w {
			t.Errorf("sawBeginSeq[%d] = %v, want %v (a failed attempt must not consume the begin marker: the reset never happened)", i, fake.sawBeginSeq[i], w)
		}
	}
	if g := mode.GetGoal().Goal; g != nil {
		t.Errorf("goal should have completed on Drive #2, still %+v", g)
	}
}

// stickyFreshAgent never fails; it completes the goal only on the Nth fresh
// turn, letting one Drive observe several successful begins/continues.
type stickyFreshAgent struct {
	mode        *goal.GoalMode
	totalTurns  int
	sawBeginSeq []bool
}

func (a *stickyFreshAgent) Run(ctx context.Context, prompt string) error {
	_, _ = a.mode.MarkComplete(goal.GoalReasonInput{}, goal.GoalActorModel)
	return nil
}

func (a *stickyFreshAgent) RunFresh(ctx context.Context, prompt string, begin bool) error {
	a.sawBeginSeq = append(a.sawBeginSeq, begin)
	if len(a.sawBeginSeq) >= a.totalTurns {
		_, _ = a.mode.MarkComplete(goal.GoalReasonInput{}, goal.GoalActorModel)
	}
	return nil
}

// TestGoalDriver_FreshContextSingleBeginWithinConversation asserts the other
// half of the contract: once a fresh-context turn has begun successfully,
// subsequent turns of the SAME goal pass begin=false (no repeated resets).
func TestGoalDriver_FreshContextSingleBeginWithinConversation(t *testing.T) {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	if _, err := mode.CreateGoal(goal.CreateGoalInput{
		Objective:    "multi-turn",
		FreshContext: true,
	}, goal.GoalActorUser); err != nil {
		t.Fatal(err)
	}
	fake := &stickyFreshAgent{mode: mode, totalTurns: 3}
	driver := &GoalDriver{Agent: fake, Mode: mode}

	if err := driver.Drive(context.Background()); err != nil {
		t.Fatalf("Drive returned %v", err)
	}
	want := []bool{true, false, false}
	if len(fake.sawBeginSeq) != len(want) {
		t.Fatalf("sawBeginSeq = %v, want %v", fake.sawBeginSeq, want)
	}
	for i, w := range want {
		if fake.sawBeginSeq[i] != w {
			t.Errorf("sawBeginSeq[%d] = %v, want %v", i, fake.sawBeginSeq[i], w)
		}
	}
}
