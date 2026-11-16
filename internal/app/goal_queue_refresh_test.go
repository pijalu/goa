// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"path/filepath"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

// goalUpdateRelay forwards a goal mode's publishes straight into the app's
// goal-update handler, standing in for the asynchronous event bus so a test
// observes the footer exactly as the running app would.
type goalUpdateRelay struct{ app *App }

func (r goalUpdateRelay) Publish(snapshot *goal.GoalSnapshot, change *goal.GoalChange) {
	r.app.handleGoalUpdate(&event.GoalUpdate{Snapshot: snapshot, Change: change})
}

// newGoalRefreshFixture builds an App with a wired goal manager (mode + queue +
// refresh hook) whose publishes land on the app handler synchronously.
func newGoalRefreshFixture(t *testing.T) (*App, *core.GoalManager, *tui.Footer) {
	t.Helper()
	a := &App{}
	footer := tui.NewFooter()
	a.subs = &subsystems{footer: footer, chat: tui.NewChatViewport()}

	mode := goal.NewGoalMode(nil, goalUpdateRelay{app: a}, nil, nil)
	mgr := core.NewGoalManagerWithMode("", mode)
	mgr.SetQueue(core.NewGoalQueueStore(filepath.Join(t.TempDir(), "queue.json")))
	a.subs.goalManager = mgr
	return a, mgr, footer
}

// TestGoalQueueMutationRefreshesFooterGoalCount is the regression test for the
// ◈ count frozen at 1: queuing goals (from the goal tool, a /goal command, or
// any other writer) mutates only the durable queue, which emits no goal
// lifecycle event. Without a refresh event the footer kept rendering the count
// from the last lifecycle event — 1 — even though goals:list showed the whole
// queue.
func TestGoalQueueMutationRefreshesFooterGoalCount(t *testing.T) {
	_, mgr, footer := newGoalRefreshFixture(t)

	if _, err := mgr.Mode.CreateGoal(goal.CreateGoalInput{Objective: "running goal"}, goal.GoalActorUser); err != nil {
		t.Fatalf("create active goal: %v", err)
	}
	if got := footer.Data().GoalCount; got != 1 {
		t.Fatalf("GoalCount after create = %d, want 1", got)
	}

	// Queue three goals the way the model's goal tool does: a plain queue
	// append, with no goal lifecycle call anywhere.
	for _, objective := range []string{"second", "third", "fourth"} {
		if _, err := mgr.Queue.AppendGoal(goal.UpcomingGoalInput{Objective: objective}); err != nil {
			t.Fatalf("queue %q: %v", objective, err)
		}
		if got := footer.Data().GoalCount; got != lenMustRead(t, mgr)+1 {
			t.Fatalf("GoalCount after queueing %q = %d, want %d (1 active + queued)", objective, got, lenMustRead(t, mgr)+1)
		}
	}

	// Removing a queued goal must refresh too (deleting from /goal:manage).
	queued, err := mgr.Queue.Read()
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := mgr.Queue.Remove(queued[0].ID); err != nil {
		t.Fatalf("remove queued goal: %v", err)
	}
	if got, want := footer.Data().GoalCount, lenMustRead(t, mgr)+1; got != want {
		t.Fatalf("GoalCount after removal = %d, want %d", got, want)
	}
}

// lenMustRead returns the current queue depth.
func lenMustRead(t *testing.T, mgr *core.GoalManager) int {
	t.Helper()
	queued, err := mgr.Queue.Read()
	if err != nil {
		t.Fatalf("read queue: %v", err)
	}
	return len(queued)
}

// TestGoalRefreshEventDoesNotPromoteQueuedGoal: a refresh event carries no
// lifecycle meaning. The handler must not promote a queued successor on it —
// a queue mutation (remove, or enqueue while no goal runs) would otherwise
// AUTO-START a goal the user only parked, breaking the "queued goals stay
// parked until resumed" contract.
func TestGoalRefreshEventDoesNotPromoteQueuedGoal(t *testing.T) {
	a, mgr, footer := newGoalRefreshFixture(t)

	for _, objective := range []string{"parked one", "parked two"} {
		if _, err := mgr.Queue.AppendGoal(goal.UpcomingGoalInput{Objective: objective}); err != nil {
			t.Fatalf("queue %q: %v", objective, err)
		}
	}
	if got := mgr.Mode.GetGoal().Goal; got != nil {
		t.Fatalf("queueing with no active goal started one: %+v", got)
	}
	if got, want := lenMustRead(t, mgr), 2; got != want {
		t.Fatalf("queue depth = %d, want %d (nothing may be consumed)", got, want)
	}
	if got := footer.Data().GoalStatus; got != "" {
		t.Errorf("footer GoalStatus = %q, want empty (no active goal)", got)
	}

	// An explicit refresh event (the shape NotifyGoalChanged emits) must not
	// promote either.
	a.handleGoalUpdate(&event.GoalUpdate{Change: &goal.GoalChange{Kind: goal.GoalChangeRefresh}})
	if got := mgr.Mode.GetGoal().Goal; got != nil {
		t.Fatalf("refresh event promoted a queued goal: %+v", got)
	}
	if got, want := lenMustRead(t, mgr), 2; got != want {
		t.Errorf("queue depth after refresh = %d, want %d", got, want)
	}
}
