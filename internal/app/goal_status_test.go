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

	footer.SetData(tui.FooterData{GoalStatus: "active"})

	a.handleGoalUpdate(&event.GoalUpdate{Snapshot: nil})

	data := footer.Data()
	if data.GoalStatus != "" {
		t.Errorf("GoalStatus = %q, want empty", data.GoalStatus)
	}
}

func newTestGoalManager() *core.GoalManager {
	mode := goal.NewGoalMode(nil, nil, nil, nil)
	mgr := core.NewGoalManagerWithMode("", mode)
	mgr.Queue = core.NewGoalQueueStore("")
	return mgr
}

// TestFooterGoalFieldsSurviveStatsRebuild (bugs.md Issues 3-4): a routine
// footer SetData (stats/activity tick without goal knowledge) must not wipe
// the ◈ goal marker.
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
	if data.GoalStatus != "active" {
		t.Errorf("goal status lost across stats rebuild: %+v", data)
	}
}
