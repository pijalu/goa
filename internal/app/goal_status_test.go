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
