// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/tui"
)

func (c *GoalCommand) queueNext(ctx core.Context, objective string, fresh bool) error {
	goals, err := c.Queue.PrependGoal(goal.UpcomingGoalInput{
		Objective:    objective,
		FreshContext: fresh,
	})
	if err != nil {
		return err
	}
	name := goals[0].Name
	if name == "" {
		writeFmt(ctx, "Queued goal to run next (queue: %d): %s\n", len(goals), objective)
	} else {
		writeFmt(ctx, "Queued goal to run next (queue: %d) [%s]: %s\n", len(goals), name, objective)
	}
	c.notifyQueueChanged()
	c.noteResumeWhenNoActiveGoal(ctx)
	return nil
}

// queueLast appends a goal to the END of the durable queue (/goal:next:last
// and the "Queue it for later" choice of the first-or-last prompt).
func (c *GoalCommand) queueLast(ctx core.Context, objective string, fresh bool) error {
	goals, err := c.Queue.AppendGoal(goal.UpcomingGoalInput{
		Objective:    objective,
		FreshContext: fresh,
	})
	if err != nil {
		return err
	}
	added := goals[len(goals)-1]
	name := added.Name
	if name == "" {
		writeFmt(ctx, "Queued goal #%d: %s\n", len(goals), objective)
	} else {
		writeFmt(ctx, "Queued goal #%d [%s]: %s\n", len(goals), name, objective)
	}
	c.notifyQueueChanged()
	c.noteResumeWhenNoActiveGoal(ctx)
	return nil
}

// noteResumeWhenNoActiveGoal appends the parked-goal hint after a queue
// insert: with no current goal the queue does not drain by itself — the
// goal stays parked (queued, paused) until the user starts it explicitly.
func (c *GoalCommand) noteResumeWhenNoActiveGoal(ctx core.Context) {
	if c.Mode.GetGoal().Goal == nil {
		writeStr(ctx, "No active goal — the goal stays queued (paused): /goal:resume to start.\n")
	}
}

// goalManagerKeymap repurposes '+'/'-' from add/delete to direct reordering
// for /goal:manage; Delete/Backspace keeps emitting the delete sentinel.
var goalManagerKeymap = tui.SelectorKeymap{ReorderMode: true}

// showQueueManager implements /goal:manage: an interactive manager listing
// the goals in EXECUTION ORDER — the active goal first (marked [active], not
// movable), then the queued goals in run order — framed by sentinel rows to
// add goals at the start/end. With the reorder keymap, '+' moves the
// highlighted goal up one position and '-' moves it down (direct hotkeys, no
// submenu); Delete/Backspace asks for confirmation before removing; 'e'
// edits the description; Enter on an add row opens the create-goal flow.
