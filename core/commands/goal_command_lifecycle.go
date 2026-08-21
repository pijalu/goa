// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"fmt"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
)

func (c *GoalCommand) pause(ctx core.Context) error {
	if err := c.rejectIfManaged("pause"); err != nil {
		return err
	}
	if c.Mode.GetGoal().Goal == nil {
		return fmt.Errorf("no current goal to pause")
	}
	reason := "paused by user"
	_, err := c.Mode.PauseGoal(goal.GoalReasonInput{Reason: &reason}, goal.GoalActorUser)
	if err != nil {
		return err
	}
	writeStr(ctx, "Goal paused.\n")
	return nil
}

// setPauseNext implements /goal:pause:next (arm) and /goal:pause:next:off
// (disarm): the pause-after-completion one-shot on the current goal. The goal
// keeps running; when it completes, the queued successor is promoted PAUSED
// instead of auto-started, so the user can review the completion evidence
// before the queue drains on (/goal:resume starts it). The flag is durable
// (patched into the goal event log) and dies with the goal.
func (c *GoalCommand) setPauseNext(ctx core.Context, armed bool) error {
	if err := c.rejectIfManaged("pause"); err != nil {
		return err
	}
	g := c.Mode.GetGoal().Goal
	if g == nil {
		return fmt.Errorf("no current goal")
	}
	if g.PauseAfterComplete == armed {
		if armed {
			writeStr(ctx, "Pause after completion is already armed — the next queued goal will be promoted paused when this goal completes.\n")
		} else {
			writeStr(ctx, "Pause after completion is not armed.\n")
		}
		return nil
	}
	if _, err := c.Mode.SetPauseAfterComplete(armed, goal.GoalActorUser); err != nil {
		return err
	}
	if armed {
		writeStr(ctx, "Pause after completion armed: this goal keeps running; when it completes, the next queued goal is promoted paused — /goal:resume to start it (/goal:pause:next:off to disarm).\n")
	} else {
		writeStr(ctx, "Pause after completion disarmed: the next queued goal auto-starts when this goal completes.\n")
	}
	return nil
}

func (c *GoalCommand) resume(ctx core.Context) error {
	if err := c.rejectIfManaged("resume"); err != nil {
		return err
	}
	if c.Mode.GetGoal().Goal == nil {
		// No current goal: take the first queued goal and move it active.
		// A queued goal must never be a dead end — previously this errored
		// with "no current goal to resume" while /goal:list showed queued
		// work the user had no way to start.
		return c.resumeFirstQueued(ctx)
	}
	_, err := c.Mode.ResumeGoal(goal.GoalReasonInput{}, goal.GoalActorUser)
	if err != nil {
		return err
	}
	writeStr(ctx, "Goal resumed.\n")
	// Resume must actually restart autonomous execution, not just flip state:
	// schedule the continuation turn the same way goal creation does, so the
	// goal proceeds without requiring a user message. Start is a no-op when a
	// drive loop is already running.
	if c.Driver != nil {
		c.Driver.Start(context.Background())
	}
	return nil
}

// resumeFirstQueued promotes the head of the durable queue straight to
// ACTIVE and kicks the driver: /goal:resume with no current goal starts the
// first queued goal (paused, enqueued) instead of erroring. Mirrors the
// app's promoteQueuedGoal (no completion handoff exists on this path — the
// queued goal's own handoff, if any, rides along).
func (c *GoalCommand) resumeFirstQueued(ctx core.Context) error {
	if c.Queue == nil {
		return fmt.Errorf("no current goal to resume")
	}
	queue, err := c.Queue.Read()
	if err != nil {
		return err
	}
	if len(queue) == 0 {
		return fmt.Errorf("no current goal to resume")
	}
	next := queue[0]
	_, removed, err := c.Queue.Remove(next.ID)
	if err != nil {
		return err
	}
	if removed == nil {
		return fmt.Errorf("no current goal to resume")
	}
	if _, err := c.Mode.CreateGoal(goal.CreateGoalInput{
		Objective:           removed.Objective,
		Name:                removed.Name,
		CompletionCriterion: removed.CompletionCriterion,
		VerifyCommand:       removed.VerifyCommand,
		FreshContext:        removed.FreshContext,
		Team:                removed.Team,
		Handoff:             removed.Handoff,
	}, goal.GoalActorUser); err != nil {
		_, _ = c.Queue.Restore(*removed)
		return err
	}
	writeFmt(ctx, "Queued goal resumed: %s\n", removed.Objective)
	// Same kick as a plain resume: flip state AND schedule the continuation
	// turn, so the goal proceeds without requiring a user message.
	if c.Driver != nil {
		c.Driver.Start(context.Background())
	}
	return nil
}

// cancel implements /goal:cancel (and /goal:cancel:current): discard the
// current goal. The clear event promotes the queued successor PAUSED — a
// cancel never auto-starts the next goal; the user resumes it explicitly.
func (c *GoalCommand) cancel(ctx core.Context) error {
	if err := c.rejectIfManaged("cancel"); err != nil {
		return err
	}
	if c.Mode.GetGoal().Goal == nil {
		return fmt.Errorf("no current goal to cancel")
	}
	// Snapshot the queue BEFORE the cancel: the successor promotion happens
	// asynchronously once the clear event crosses the bus.
	queued := 0
	if c.Queue != nil {
		if q, err := c.Queue.Read(); err == nil {
			queued = len(q)
		}
	}
	_, err := c.Mode.CancelGoal(goal.GoalActorUser)
	if err != nil {
		return err
	}
	// A cancelled goal has nothing left to execute: stop the drive loop so
	// the in-flight continuation turn is interrupted (its context is the
	// loop's) and no further continuation turns launch — the "Answering..."
	// spinner must not survive the cancel. Mirrors the goal half of the ESC
	// hard stop (handleEscape). Deliberately NOT AgentManager.Interrupt: a
	// user-owned turn in flight is not the goal's business. Order matters:
	// CancelGoal first clears the state, so the interrupted turn's error
	// handling (PauseActiveGoal) no-ops instead of pausing a dead goal.
	if c.Driver != nil {
		c.Driver.Stop()
	}
	writeStr(ctx, "Goal cancelled.\n")
	if queued > 0 {
		writeStr(ctx, "The next queued goal is promoted paused — /goal:resume to start it.\n")
	}
	return nil
}

// cancelAll implements /goal:cancel:all: discard the current goal AND clear
// every queued goal. The queue is cleared FIRST: queue operations emit no
// goal events, so the clear event's successor promotion (async, on the
// event-forwarder goroutine) then finds an empty queue and stays a no-op.
func (c *GoalCommand) cancelAll(ctx core.Context) error {
	if err := c.rejectIfManaged("cancel"); err != nil {
		return err
	}
	cleared := 0
	if c.Queue != nil {
		goals, err := c.Queue.Clear()
		if err != nil {
			return err
		}
		cleared = len(goals)
		c.notifyQueueChanged()
	}
	if c.Mode.GetGoal().Goal == nil {
		if cleared == 0 {
			return fmt.Errorf("nothing to cancel: no current goal and the queue is empty")
		}
		writeFmt(ctx, "Cleared %d queued goal(s).\n", cleared)
		return nil
	}
	if _, err := c.Mode.CancelGoal(goal.GoalActorUser); err != nil {
		return err
	}
	if c.Driver != nil {
		c.Driver.Stop()
	}
	writeFmt(ctx, "Goal cancelled; %d queued goal(s) cleared.\n", cleared)
	return nil
}

// goalLogLimit caps how many event-log records /goal:log renders.
const goalLogLimit = 20

// showEventLog implements /goal:log: render the recent goal event records
// (time, type, actor, status, reason, expectation) from the durable log.
