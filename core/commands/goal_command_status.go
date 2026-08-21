// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
)

func (c *GoalCommand) showStatus(ctx core.Context) error {
	result := c.Mode.GetGoal()
	if result.Goal == nil {
		writeStr(ctx, "No current goal.\n")
		return nil
	}
	g := result.Goal
	name := g.Name
	if g.ManagedBy != "" {
		name += " [" + g.ManagedBy + "]"
	}
	writeFmt(ctx, "Goal [%s]: %s\n", name, g.Objective)
	writeFmt(ctx, "Status: %s\n", g.Status)
	writeFmt(ctx, "Turns: %d\n", g.TurnsUsed)
	writeFmt(ctx, "Tokens: %s\n", goal.FormatTokens(g.TokensUsed))
	writeFmt(ctx, "Elapsed: %s\n", goal.FormatElapsed(g.WallClockMs))
	if g.PauseAfterComplete {
		writeStr(ctx, "Pause after completion: armed (next queued goal promotes paused)\n")
	}
	return nil
}

// showCurrent implements /goal:current: print the currently executed goal
// with its full (untruncated) objective, completion criterion, verify command
// and todo list with statuses — richer than /goal:status, which shows only
// counters. Rendered as markdown so it formats in the chat panel.
func (c *GoalCommand) showCurrent(ctx core.Context) error {
	result := c.Mode.GetGoal()
	if result.Goal == nil {
		writeStr(ctx, "No current goal.\n")
		return nil
	}
	g := result.Goal
	var sb strings.Builder
	name := g.Name
	if name == "" {
		name = "(unnamed)"
	}
	if g.ManagedBy != "" {
		name += " [" + g.ManagedBy + "]"
	}
	fmt.Fprintf(&sb, "**%s** — status %s · turns %d · %s tokens · %s\n\n",
		name, g.Status, g.TurnsUsed, goal.FormatTokens(g.TokensUsed), goal.FormatElapsed(g.WallClockMs))
	fmt.Fprintf(&sb, "%s\n\n", g.Objective)
	if g.CompletionCriterion != nil {
		fmt.Fprintf(&sb, "- Completion criterion: %s\n", *g.CompletionCriterion)
	}
	if g.VerifyCommand != nil {
		fmt.Fprintf(&sb, "- Verify command: `%s`\n", *g.VerifyCommand)
	}
	if g.CompletionCriterion != nil || g.VerifyCommand != nil {
		sb.WriteString("\n")
	}
	if g.PauseAfterComplete {
		sb.WriteString("- Pause after completion: armed (`/goal:pause:next:off` to disarm)\n\n")
	}
	writeGoalTodos(&sb, g.Todos)
	writeStr(ctx, sb.String())
	return nil
}

// writeGoalTodos renders the todo list with status markers, one per line.
func writeGoalTodos(sb *strings.Builder, todos []goal.GoalTodoItem) {
	if len(todos) == 0 {
		return
	}
	done := 0
	for _, td := range todos {
		if td.Status == goal.TodoDone {
			done++
		}
	}
	fmt.Fprintf(sb, "Todos (%d/%d done):\n\n", done, len(todos))
	for _, td := range todos {
		fmt.Fprintf(sb, "- %s %s\n", todoStatusMark(td.Status), td.Title)
	}
}

// todoStatusMark renders a todo status as a checkbox-style marker.
func todoStatusMark(status string) string {
	switch status {
	case goal.TodoDone:
		return "[x]"
	case goal.TodoInProgress:
		return "[>]"
	default:
		return "[ ]"
	}
}

// showList implements /goal:list: list the current goal and every queued goal
// in execution order, showing ALL information recorded for each goal —
// placement, name, status, counters, context run type (fresh/reuse),
// completion criterion, verify command, handover, budget, terminal state and
// todos — plus the COMPLETE objective (no truncation). The output is markdown
// so it renders formatted in the chat panel — the counterpart to the goal
// bubble, which caps its display at 3 lines.
func (c *GoalCommand) showList(ctx core.Context) error {
	active := c.Mode.GetGoal().Goal
	queued, err := c.Queue.Read()
	if err != nil {
		return err
	}
	if active == nil && len(queued) == 0 {
		writeStr(ctx, "No goals.\n")
		return nil
	}

	var sb strings.Builder
	sb.WriteString("## Goals\n\n")
	order := 1
	if active != nil {
		writeCurrentGoalListEntry(&sb, order, active)
		order++
	}
	for i := range queued {
		writeQueuedGoalListEntry(&sb, order, &queued[i])
		order++
	}
	writeStr(ctx, sb.String())
	return nil
}

// writeGoalListEntry renders one goal as a markdown list item: a bold header
// with order number, placement and name, an optional metadata line, then the
// complete untruncated objective on its own line. The parts are separated by
// blank lines so the markdown renderer keeps them as DISTINCT blocks —
// consecutive plain lines would soft-join into a single paragraph
// "Goal list issue"). Blank separator lines are dropped by the renderer, so
// this costs no extra rows.
func writeGoalListEntry(sb *strings.Builder, order int, placement, name, meta, objective string) {
	if name == "" {
		name = "(unnamed)"
	}
	fmt.Fprintf(sb, "**%d. [%s] %s**\n\n", order, placement, name)
	if meta != "" {
		fmt.Fprintf(sb, "*%s*\n\n", meta)
	}
	fmt.Fprintf(sb, "%s\n\n", objective)
}

// contextRunLabel renders the goal's context run type: "fresh" runs the
// continuation turns on a clean context (objective + handover only), "reuse"
// reuses the current conversation.
func contextRunLabel(fresh bool) string {
	if fresh {
		return "fresh"
	}
	return "reuse"
}

// formatGoalBudget renders a compact "budget used/limit" summary for goals
// with hard limits; unlimited goals yield "".
func formatGoalBudget(g *goal.GoalSnapshot) string {
	parts := make([]string, 0, 3)
	if g.Budget.TurnBudget != nil {
		parts = append(parts, fmt.Sprintf("turns %d/%d", g.TurnsUsed, *g.Budget.TurnBudget))
	}
	if g.Budget.TokenBudget != nil {
		parts = append(parts, fmt.Sprintf("tokens %s/%s", goal.FormatTokens(g.TokensUsed), goal.FormatTokens(*g.Budget.TokenBudget)))
	}
	if g.Budget.WallClockBudgetMs != nil {
		parts = append(parts, fmt.Sprintf("time %s/%s", goal.FormatElapsed(g.WallClockMs), goal.FormatElapsed(*g.Budget.WallClockBudgetMs)))
	}
	if len(parts) == 0 {
		return ""
	}
	return "budget " + strings.Join(parts, " · ")
}

// writeGoalAttrs renders the optional attributes shared by current and queued
// goals — completion criterion, verify command and handover — as one markdown
// bullet block. A set handover is shown as a presence marker only: its text
// is untrusted free-form content carried into the goal's reminder, not list
// material.
func writeGoalAttrs(sb *strings.Builder, criterion, verifyCommand, handover *string) {
	wrote := false
	if criterion != nil {
		fmt.Fprintf(sb, "- Completion criterion: %s\n", *criterion)
		wrote = true
	}
	if verifyCommand != nil {
		fmt.Fprintf(sb, "- Verify command: `%s`\n", *verifyCommand)
		wrote = true
	}
	if handover != nil {
		sb.WriteString("- Handover: attached\n")
		wrote = true
	}
	if wrote {
		sb.WriteString("\n")
	}
}

// writeGoalTerminalState renders the terminal reason/expectation of a paused
// or blocked goal as a markdown bullet block. Goals without a terminal state
// yield nothing.
func writeGoalTerminalState(sb *strings.Builder, g *goal.GoalSnapshot) {
	wrote := false
	if g.TerminalReason != nil {
		fmt.Fprintf(sb, "- Reason: %s\n", *g.TerminalReason)
		wrote = true
	}
	if g.TerminalExpectation != nil {
		fmt.Fprintf(sb, "- Needs: %s\n", *g.TerminalExpectation)
		wrote = true
	}
	if wrote {
		sb.WriteString("\n")
	}
}

// writeCurrentGoalListEntry renders the current goal (whatever its status)
// with its full information: counters, context run type, budget, criterion,
// verify command, handover, terminal state and todos.
func writeCurrentGoalListEntry(sb *strings.Builder, order int, g *goal.GoalSnapshot) {
	name := g.Name
	if g.ManagedBy != "" {
		name += " [" + g.ManagedBy + "]"
	}
	meta := fmt.Sprintf("status %s · turns %d · %s tokens · %s · context %s",
		g.Status, g.TurnsUsed, goal.FormatTokens(g.TokensUsed), goal.FormatElapsed(g.WallClockMs), contextRunLabel(g.FreshContext))
	if g.PauseAfterComplete {
		meta += " · pause-after-complete armed"
	}
	if budget := formatGoalBudget(g); budget != "" {
		meta += " · " + budget
	}
	writeGoalListEntry(sb, order, "active", name, meta, g.Objective)
	writeGoalAttrs(sb, g.CompletionCriterion, g.VerifyCommand, g.Handoff)
	writeGoalTerminalState(sb, g)
	if len(g.Todos) > 0 {
		writeGoalTodos(sb, g.Todos)
		sb.WriteString("\n")
	}
}

// writeQueuedGoalListEntry renders a queued goal with its full information:
// context run type, queue time, criterion, verify command and handover.
func writeQueuedGoalListEntry(sb *strings.Builder, order int, g *goal.UpcomingGoal) {
	name := g.Name
	if g.ManagedBy != "" {
		name += " [" + g.ManagedBy + "]"
	}
	meta := "context " + contextRunLabel(g.FreshContext)
	if !g.CreatedAt.IsZero() {
		meta += " · queued " + g.CreatedAt.Format("2006-01-02 15:04")
	}
	writeGoalListEntry(sb, order, "queued", name, meta, g.Objective)
	writeGoalAttrs(sb, g.CompletionCriterion, g.VerifyCommand, g.Handoff)
}
