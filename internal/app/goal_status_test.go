// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/tui"
)

func TestHandleGoalUpdate_SetsFooter(t *testing.T) {
	a := &App{}
	footer := tui.NewFooter()
	chat := tui.NewChatViewport()
	a.subs = &subsystems{
		footer:      footer,
		chat:        chat,
		goalManager: newTestGoalManager(),
	}

	snap := &goal.GoalSnapshot{
		Objective: "Test goal",
		Status:    goal.GoalActive,
	}
	a.handleGoalUpdate(&event.GoalUpdate{Snapshot: snap})

	data := footer.Data()
	if data.GoalStatus != "active" {
		t.Errorf("GoalStatus = %q, want active", data.GoalStatus)
	}
	if data.GoalObjective != "Test goal" {
		t.Errorf("GoalObjective = %q, want Test goal", data.GoalObjective)
	}
}

func TestHandleGoalUpdate_ClearResetsFooter(t *testing.T) {
	a := &App{}
	footer := tui.NewFooter()
	chat := tui.NewChatViewport()
	a.subs = &subsystems{
		footer:      footer,
		chat:        chat,
		goalManager: newTestGoalManager(),
	}

	footer.SetData(tui.FooterData{GoalStatus: "active", GoalObjective: "Old"})

	a.handleGoalUpdate(&event.GoalUpdate{Snapshot: nil})

	data := footer.Data()
	if data.GoalStatus != "" {
		t.Errorf("GoalStatus = %q, want empty", data.GoalStatus)
	}
	if data.GoalObjective != "" {
		t.Errorf("GoalObjective = %q, want empty", data.GoalObjective)
	}
}

func newTestGoalManager() *core.GoalManager {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mgr := core.NewGoalManagerWithMode("", mode)
	mgr.Queue = core.NewGoalQueueStore("")
	return mgr
}

// TestHandleGoalUpdate_CountsPendingTodos (bugs.md Issue 4): the footer's
// pending-todo count drives the ⬩ markers — done todos don't count.
func TestHandleGoalUpdate_CountsPendingTodos(t *testing.T) {
	a := &App{}
	footer := tui.NewFooter()
	chat := tui.NewChatViewport()
	a.subs = &subsystems{footer: footer, chat: chat, goalManager: newTestGoalManager()}

	snap := &goal.GoalSnapshot{
		Objective: "x",
		Status:    goal.GoalActive,
		Todos: []goal.GoalTodoItem{
			{ID: "t1", Title: "a", Status: goal.TodoDone},
			{ID: "t2", Title: "b", Status: goal.TodoPending},
			{ID: "t3", Title: "c", Status: goal.TodoInProgress},
			{ID: "t4", Title: "d", Status: goal.TodoPending},
			{ID: "t5", Title: "e", Status: goal.TodoPending},
		},
	}
	a.handleGoalUpdate(&event.GoalUpdate{Snapshot: snap})
	if got := footer.Data().GoalPendingTodos; got != 4 {
		t.Errorf("GoalPendingTodos = %d, want 4 (done excluded)", got)
	}

	a.handleGoalUpdate(&event.GoalUpdate{Snapshot: nil})
	if got := footer.Data().GoalPendingTodos; got != 0 {
		t.Errorf("GoalPendingTodos after clear = %d, want 0", got)
	}
}

// TestFooterGoalFieldsSurviveStatsRebuild (bugs.md Issues 3-4): a routine
// footer SetData (stats/activity tick without goal knowledge) must not wipe
// the ◈/⬩ goal markers.
func TestFooterGoalFieldsSurviveStatsRebuild(t *testing.T) {
	a := &App{}
	footer := tui.NewFooter()
	chat := tui.NewChatViewport()
	a.subs = &subsystems{footer: footer, chat: chat, goalManager: newTestGoalManager()}

	a.handleGoalUpdate(&event.GoalUpdate{Snapshot: &goal.GoalSnapshot{
		Objective: "keep me", Status: goal.GoalActive,
		Todos: []goal.GoalTodoItem{{ID: "t1", Title: "x", Status: goal.TodoPending}},
	}})

	// Routine stats rebuild — no goal fields in the payload.
	footer.SetData(tui.FooterData{Stats: "↑1k ↓2k 50%"})

	data := footer.Data()
	if data.GoalStatus != "active" || data.GoalObjective != "keep me" || data.GoalPendingTodos != 1 {
		t.Errorf("goal fields lost across stats rebuild: %+v", data)
	}
}
