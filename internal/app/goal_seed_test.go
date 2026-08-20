// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"
	"time"

	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
	goaltui "github.com/pijalu/goa/tui/goal"
)

// TestSeedGoalUI_RestoresBubbleAndFooter reproduces Issue 1: an app
// restarted with a persisted goal never shows the goal bubble, because the
// bubble is only fed by live GoalUpdate bus events and no event fires at
// startup. Seeding from the goal manager must restore both the bubble and
// the footer goal fields.
func TestSeedGoalUI_RestoresBubbleAndFooter(t *testing.T) {
	mgr := newTestGoalManager()
	if _, err := mgr.Mode.CreateGoal(goal.CreateGoalInput{Objective: "persisted goal"}, goal.GoalActorUser); err != nil {
		t.Fatalf("CreateGoal: %v", err)
	}

	footer := tui.NewFooter()
	bubble := goaltui.NewBubble()
	a := &App{}
	a.subs = &subsystems{
		footer:      footer,
		goalBubble:  bubble,
		goalManager: mgr,
	}

	a.seedGoalUI()

	snap := bubble.Snapshot()
	if snap == nil {
		t.Fatal("bubble snapshot nil after seed; want persisted goal")
	}
	if snap.Objective != "persisted goal" {
		t.Errorf("bubble objective = %q, want %q", snap.Objective, "persisted goal")
	}
	data := footer.Data()
	if data.GoalStatus != string(goal.GoalActive) {
		t.Errorf("footer GoalStatus = %q, want %q", data.GoalStatus, goal.GoalActive)
	}
}

// TestSeedGoalUI_NoGoalKeepsUIClear guards the seed path when no goal
// exists: bubble and footer must stay clear (no phantom goal).
func TestSeedGoalUI_NoGoalKeepsUIClear(t *testing.T) {
	footer := tui.NewFooter()
	bubble := goaltui.NewBubble()
	a := &App{}
	a.subs = &subsystems{
		footer:      footer,
		goalBubble:  bubble,
		goalManager: newTestGoalManager(),
	}

	a.seedGoalUI()

	if bubble.Snapshot() != nil {
		t.Errorf("bubble snapshot = %+v, want nil", bubble.Snapshot())
	}
	if footer.Data().GoalStatus != "" {
		t.Errorf("footer GoalStatus = %q, want empty", footer.Data().GoalStatus)
	}
}

// TestGoalEventPublisher_FullBusDoesNotDrop reproduces Issue 1(b):
// goalEventPublisher.Publish used a non-blocking send and silently dropped
// the update when the Agent bus was full — exactly the mid-turn situation
// where a goal create/resume happens. The update must eventually arrive.
func TestGoalEventPublisher_FullBusDoesNotDrop(t *testing.T) {
	bus := event.MakeBus(1, 1, 1, 1)
	bus.Agent <- event.AgentEvent{} // fill the single slot
	p := &goalEventPublisher{bus: bus}

	snap := &goal.GoalSnapshot{Objective: "x", Status: goal.GoalActive}
	p.Publish(snap, nil)

	<-bus.Agent // drain the filler, freeing room for the pending update
	select {
	case ev := <-bus.Agent:
		if ev.GoalUpdate == nil {
			t.Fatal("received event without GoalUpdate")
		}
		if ev.GoalUpdate.Snapshot == nil || ev.GoalUpdate.Snapshot.Objective != "x" {
			t.Fatalf("GoalUpdate.Snapshot = %+v, want objective x", ev.GoalUpdate.Snapshot)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("goal update dropped on full bus")
	}
}

// TestGoalEventPublisher_OrderPreservedUnderLoad asserts that updates
// published while the bus is full arrive in publish order (create → clear),
// so the bubble never ends up showing a stale goal after a later clear.
func TestGoalEventPublisher_OrderPreservedUnderLoad(t *testing.T) {
	bus := event.MakeBus(1, 1, 1, 1)
	bus.Agent <- event.AgentEvent{} // fill
	p := &goalEventPublisher{bus: bus}

	snap := &goal.GoalSnapshot{Objective: "g1", Status: goal.GoalActive}
	p.Publish(snap, nil) // 1: create
	p.Publish(nil, nil)  // 2: clear

	<-bus.Agent // drain filler
	got := make([]*goal.GoalSnapshot, 0, 2)
	timeout := time.After(2 * time.Second)
	for len(got) < 2 {
		select {
		case ev := <-bus.Agent:
			if ev.GoalUpdate == nil {
				t.Fatal("received event without GoalUpdate")
			}
			got = append(got, ev.GoalUpdate.Snapshot)
		case <-timeout:
			t.Fatalf("only %d/2 updates delivered", len(got))
		}
	}
	if got[0] == nil || got[0].Objective != "g1" {
		t.Errorf("first update = %+v, want create g1", got[0])
	}
	if got[1] != nil {
		t.Errorf("second update = %+v, want clear (nil snapshot)", got[1])
	}
}
