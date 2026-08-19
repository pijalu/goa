// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/tui"
)

func (c *GoalCommand) create(ctx core.Context, objective string, fresh bool) error {
	if c.Mode.GetGoal().Goal != nil {
		return c.promptFirstOrLast(ctx, objective, fresh)
	}
	return c.startGoal(ctx, objective, false, fresh)
}

// replace handles /goal:replace:<text>. It asks for confirmation before
// discarding the current goal, then proceeds through the autonomy permission
// flow.
func (c *GoalCommand) replace(ctx core.Context, objective string) error {
	current := c.Mode.GetGoal().Goal
	if current == nil {
		return fmt.Errorf("no current goal to replace")
	}
	if err := c.rejectIfManaged("replace"); err != nil {
		return err
	}
	c.promptReplaceConfirm(ctx, current, objective)
	return nil
}

// promptFirstOrLast asks the user where to put a new goal when one is already
// active (item 4). "First/active" replaces the current goal; "Last" queues it.
// Per the UX guideline, the active goal's details are shown FIRST so the user
// can decide what to do with the running goal.
func (c *GoalCommand) promptFirstOrLast(ctx core.Context, objective string, fresh bool) error {
	current := c.Mode.GetGoal().Goal
	c.describeActiveGoal(ctx, current)
	activeLabel := "<current goal>"
	if current != nil && current.Name != "" {
		activeLabel = current.Name
	}
	opts := []tui.SelectorItem{
		{Value: "first", Label: "Replace the active goal", Description: fmt.Sprintf("Discard %s and start the new goal now.", activeLabel)},
		{Value: "last", Label: "Queue it for later", Description: "Append to the queue; runs after the current goal completes."},
		{Value: "cancel", Label: "Do not create", Description: "Return to the input box."},
	}
	ctx.SelectOption("A goal is already active — where should the new goal go?", opts, "", func(selected string, ok bool) {
		if !ok || selected == "cancel" {
			return
		}
		switch selected {
		case "first":
			_ = c.startGoal(ctx, objective, true, fresh)
		case "last":
			_ = c.queueLast(ctx, objective, fresh)
		}
	})
	return nil
}

// promptReplaceConfirm asks the user to confirm replacing the active goal.
func (c *GoalCommand) promptReplaceConfirm(ctx core.Context, current *goal.GoalSnapshot, objective string) {
	activeLabel := current.Name
	if activeLabel == "" {
		activeLabel = "<current goal>"
	}
	opts := []tui.SelectorItem{
		{Value: "replace", Label: "Yes, replace it", Description: fmt.Sprintf("Discard %s and start the new goal.", activeLabel)},
		{Value: "cancel", Label: "No, keep it", Description: "Return to the input box."},
	}
	title := fmt.Sprintf("Replace goal %s (%s) with: %s", activeLabel, truncate(current.Objective, 40), truncate(objective, 60))
	ctx.SelectOption(title, opts, "", func(selected string, ok bool) {
		if !ok || selected == "cancel" {
			return
		}
		_ = c.startGoal(ctx, objective, true, c.resolveFresh(""))
	})
}

func (c *GoalCommand) rejectIfManaged(op string) error {
	g := c.Mode.GetGoal().Goal
	if g != nil && g.ManagedBy == "orchestrator" {
		return fmt.Errorf("goal %s is managed by /orchestrate; cannot %s", g.Name, op)
	}
	return nil
}

// describeActiveGoal writes a short summary of the currently active goal to
// the output so the user has context before being asked what to do next.
// No-op when there is no active goal.
func (c *GoalCommand) describeActiveGoal(ctx core.Context, g *goal.GoalSnapshot) {
	if g == nil {
		return
	}
	name := g.Name
	if name == "" {
		name = "<unnamed>"
	}
	writeFmt(ctx, "Active goal [%s]: %s\n", name, g.Objective)
	writeFmt(ctx, "Status: %s | Turns: %d | Tokens: %s | Elapsed: %s\n",
		g.Status, g.TurnsUsed, goal.FormatTokens(g.TokensUsed), goal.FormatElapsed(g.WallClockMs))
}

// promptCreateInteractive drives the interactive create/queue flow via the
// main input line: ctrl-c (or empty) aborts; a typed objective proceeds
// according to placement (front of queue, end of queue, replace, or — for
// placementAsk — the first/last prompt follows). fresh carries the context
// mode resolved from the command token (/goal:next:reuse with no text must
// still honor reuse once the objective is typed).
func (c *GoalCommand) promptCreateInteractive(ctx core.Context, placement goalPlacement, fresh bool) error {
	promptText := "Set new goal objective (ctrl-c to cancel)"
	switch placement {
	case placementNext:
		promptText = "Queue a goal to run next — right after the active goal (ctrl-c to cancel)"
	case placementLast:
		promptText = "Queue a goal at the end of the queue (ctrl-c to cancel)"
	}
	if ctx.RequestMainInput == nil {
		return fmt.Errorf("main input not available")
	}
	ctx.RequestMainInput(promptText, func(value string) {
		objective := strings.TrimSpace(value)
		if objective == "" {
			return
		}
		switch placement {
		case placementNext:
			_ = c.queueNext(ctx, objective, fresh)
		case placementLast:
			_ = c.queueLast(ctx, objective, fresh)
		case placementFirst:
			_ = c.startGoal(ctx, objective, true, fresh)
		default: // placementAsk
			_ = c.create(ctx, objective, fresh)
		}
	})
	return nil
}

// promptReplaceInteractive drives /goal:replace without text: asks for the
// objective on the main input line, then confirms before replacing.
func (c *GoalCommand) promptReplaceInteractive(ctx core.Context) error {
	current := c.Mode.GetGoal().Goal
	if current == nil {
		return fmt.Errorf("no current goal to replace")
	}
	if ctx.RequestMainInput == nil {
		return fmt.Errorf("main input not available")
	}
	ctx.RequestMainInput("Replace active goal with objective (ctrl-c to cancel)", func(value string) {
		objective := strings.TrimSpace(value)
		if objective == "" {
			return
		}
		c.promptReplaceConfirm(ctx, current, objective)
	})
	return nil
}

func (c *GoalCommand) startGoal(ctx core.Context, objective string, replace bool, fresh bool) error {
	if c.AutonomySwitcher != nil {
		level := c.AutonomySwitcher.Current()
		if level != internal.AutonomyYolo {
			c.promptStartPermission(ctx, objective, replace, level, fresh)
			return nil
		}
	}
	return c.doStartGoal(ctx, objective, replace, fresh)
}

func (c *GoalCommand) promptStartPermission(ctx core.Context, objective string, replace bool, current internal.AutonomyLevel, fresh bool) {
	opts := c.permissionOptions(current)
	ctx.SelectOption("Start a goal?", opts, "", func(selected string, ok bool) {
		if !ok {
			return
		}
		switch selected {
		case "auto":
			_ = c.AutonomySwitcher.SetAutonomy(internal.AutonomyConfirm)
		case "yolo":
			_ = c.AutonomySwitcher.SetAutonomy(internal.AutonomyYolo)
		case "manual":
			_ = c.AutonomySwitcher.SetAutonomy(internal.AutonomyConfirm)
		case "cancel":
			ctx.Flash("Goal start cancelled.")
			return
		}
		_ = c.doStartGoal(ctx, objective, replace, fresh)
	})
}

func (c *GoalCommand) permissionOptions(current internal.AutonomyLevel) []tui.SelectorItem {
	if current == internal.AutonomyYolo {
		return []tui.SelectorItem{
			{Value: "auto", Label: "Switch to Auto and start", Description: "Tools approved automatically, questions skipped."},
			{Value: "yolo", Label: "Keep YOLO and start", Description: "Tools auto-approved, model may still ask questions."},
			{Value: "cancel", Label: "Do not start", Description: "Return to the input box."},
		}
	}
	return []tui.SelectorItem{
		{Value: "auto", Label: "Switch to Auto and start", Description: "Best for unattended work. Tools are approved automatically."},
		{Value: "yolo", Label: "Switch to YOLO and start", Description: "Tools auto-approved, model may still ask questions."},
		{Value: "manual", Label: "Start in Manual", Description: "Goal may stop and wait for your approval. Not suitable for unattended goal work."},
		{Value: "cancel", Label: "Do not start", Description: "Return to the input box."},
	}
}

func (c *GoalCommand) doStartGoal(ctx core.Context, objective string, replace bool, fresh bool) error {
	// Creation entry point: reject an oversized objective with the
	// point-to-a-markdown-doc hint BEFORE it enters the system.
	if err := goal.ValidateObjective(objective); err != nil {
		ctx.Flash(err.Error())
		return err
	}
	snap, err := c.Mode.CreateGoal(goal.CreateGoalInput{
		Objective:    objective,
		Replace:      replace,
		FreshContext: fresh,
	}, goal.GoalActorUser)
	if err != nil {
		ctx.Flash(err.Error())
		return err
	}
	name := snap.Name
	ctx.Flash("Started goal: " + snap.Objective)
	if name != "" {
		writeFmt(ctx, "Started goal [%s]: %s\n", name, snap.Objective)
	} else {
		writeFmt(ctx, "Started goal: %s\n", snap.Objective)
	}
	if c.Driver != nil {
		c.Driver.Start(context.Background())
	}
	return nil
}
