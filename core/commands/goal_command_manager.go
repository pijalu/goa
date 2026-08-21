// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/tui"
)

func (c *GoalCommand) showQueueManager(ctx core.Context) error {
	return c.showQueueManagerAt(ctx, "")
}

// showQueueManagerAt opens the manager with the cursor (and ✓ marker) on the
// row with the given value — every hotkey emit closes the selector, so the
// manager reopens after each action and cursor keeps it on the row the user
// is working with ("" starts on the first row).
func (c *GoalCommand) showQueueManagerAt(ctx core.Context, cursor string) error {
	queued, err := c.Queue.Read()
	if err != nil {
		return err
	}
	items := c.managerItems(queued)
	ctx.SelectOptionKeyed("Goal manager — execution order", items, cursor, goalManagerKeymap, func(selected string, ok bool) {
		c.handleManagerSelection(ctx, selected, ok)
	})
	return nil
}

// managerItems builds the manager rows in execution order: the add-at-start
// sentinel, the active goal (marked, if any), the queued goals in run order,
// the add-at-end sentinel and the Done row. PreserveOrder on every row keeps
// the selector in caller order — the default alphabetical Label sort would
// scramble the execution order the manager is meant to show. Goal rows opt
// into the 'e' edit hotkey via Editable.
func (c *GoalCommand) managerItems(queued []goal.UpcomingGoal) []tui.SelectorItem {
	items := make([]tui.SelectorItem, 0, len(queued)+4)
	items = append(items, tui.SelectorItem{
		Value: "__add_first__", Label: "-- add at start --",
		Description: "queue a goal to run next", PreserveOrder: true,
	})
	if active := c.Mode.GetGoal().Goal; active != nil {
		items = append(items, tui.SelectorItem{
			Value:         "__active__",
			Label:         "[active] " + goalRowLabel(active.Name, active.Objective),
			Description:   "running — not reorderable here",
			PreserveOrder: true,
		})
	}
	for i, g := range queued {
		items = append(items, tui.SelectorItem{
			Value:         g.ID,
			Label:         goalRowLabel(g.Name, g.Objective),
			Description:   fmt.Sprintf("%c", 'A'+i),
			Editable:      true,
			PreserveOrder: true,
		})
	}
	items = append(items,
		tui.SelectorItem{Value: "__add_last__", Label: "-- add at end --",
			Description: "queue a goal to run last", PreserveOrder: true},
		tui.SelectorItem{Value: "__done__", Label: "Done",
			Description: "Close goal manager", PreserveOrder: true},
	)
	return items
}

// goalRowLabel renders one goal row as "name — objective…" with the
// objective truncated to fit the selector width.
func goalRowLabel(name, objective string) string {
	label := truncate(objective, 60)
	if name != "" {
		label = fmt.Sprintf("%s — %s", name, label)
	}
	return label
}

// handleManagerSelection dispatches one selector emit from the manager:
// hotkey emits (move up/down, delete, edit), the add rows, the active-goal
// row, and plain Enter on a goal row.
func (c *GoalCommand) handleManagerSelection(ctx core.Context, selected string, ok bool) {
	if !ok || selected == "__done__" {
		return
	}
	if id, yes := strings.CutPrefix(selected, "__moveup__"); yes {
		c.moveManagerGoal(ctx, id, "up")
		return
	}
	if id, yes := strings.CutPrefix(selected, "__movedown__"); yes {
		c.moveManagerGoal(ctx, id, "down")
		return
	}
	if id, yes := strings.CutPrefix(selected, "__delete__"); yes {
		c.confirmDeleteManagerGoal(ctx, id)
		return
	}
	// The selector's 'e' hotkey emits "__edit__"+id for the highlighted goal
	// (goal rows opt in via SelectorItem.Editable): prompt for the new
	// description instead of treating the sentinel as a goal id.
	if id, yes := strings.CutPrefix(selected, "__edit__"); yes {
		c.promptEditQueuedGoal(ctx, id)
		return
	}
	switch selected {
	case "__add_first__":
		c.promptCreateForPlacement(ctx, placementNext)
	case "__add_last__":
		c.promptCreateForPlacement(ctx, placementLast)
	case "__add__":
		// Generic '+' emit — only reachable through a host without the
		// reorder keymap (SelectOptionKeyed fallback). Route it to the add
		// flow: it previously fell through to the queue-action menu and
		// failed with "queued goal … not found" (goal manager).
		c.promptCreateForPlacement(ctx, placementLast)
	case "__active__":
		ctx.Flash("The active goal is running — use /goal:pause, /goal:cancel or /goal:replace.")
		_ = c.showQueueManagerAt(ctx, "__active__")
	default:
		// Enter on a queued goal: reorder and delete are hotkey-driven now
		// (the two-step action menu is gone) — remind and reopen.
		ctx.Flash("Hotkeys: '+' move up · '-' move down · 'e' edit · del delete (with confirmation)")
		_ = c.showQueueManagerAt(ctx, selected)
	}
}

// moveManagerGoal implements the '+/-' reorder hotkeys: move the goal one
// position and reopen the manager with the cursor on it, so repeated presses
// keep moving the same goal. The active goal is not movable from the manager.
func (c *GoalCommand) moveManagerGoal(ctx core.Context, id, direction string) {
	if id == "__active__" {
		ctx.Flash("The active goal is running — queued goals reorder around it.")
		_ = c.showQueueManagerAt(ctx, "__active__")
		return
	}
	if _, err := c.Queue.Move(id, direction); err != nil {
		ctx.Flash(err.Error())
		_ = c.showQueueManagerAt(ctx, "")
		return
	}
	c.notifyQueueChanged()
	_ = c.showQueueManagerAt(ctx, id)
}

// confirmDeleteManagerGoal implements the Delete/Backspace hotkey: deletion
// asks for confirmation before the goal is removed (previously it was
// removed immediately). Yes removes and reopens the manager; No (or Escape)
// returns to the manager with the cursor back on the goal. The active goal
// cannot be removed here — it goes through /goal:cancel.
func (c *GoalCommand) confirmDeleteManagerGoal(ctx core.Context, id string) {
	if id == "__active__" {
		ctx.Flash("The active goal is running — cancel it with /goal:cancel (or /goal:cancel:all).")
		_ = c.showQueueManagerAt(ctx, "__active__")
		return
	}
	label := c.managerGoalLabel(ctx, id)
	if label == "" {
		_ = c.showQueueManagerAt(ctx, "")
		return
	}
	opts := []tui.SelectorItem{
		{Value: "yes", Label: "Yes, delete it"},
		{Value: "no", Label: "No, keep it"},
	}
	ctx.SelectOption("Delete goal "+label+"?", opts, "no", func(v string, ok bool) {
		if !ok || v != "yes" {
			_ = c.showQueueManagerAt(ctx, id)
			return
		}
		if _, _, err := c.Queue.Remove(id); err != nil {
			ctx.Flash(err.Error())
		} else {
			c.notifyQueueChanged()
			ctx.Flash("Goal deleted.")
		}
		_ = c.showQueueManagerAt(ctx, "")
	})
}

// managerGoalLabel returns the display label of a queued goal for the
// delete-confirmation title, or "" (after flashing) when it is not queued.
func (c *GoalCommand) managerGoalLabel(ctx core.Context, id string) string {
	goals, err := c.Queue.Read()
	if err != nil {
		ctx.Flash(err.Error())
		return ""
	}
	for _, g := range goals {
		if g.ID == id {
			return goalRowLabel(g.Name, g.Objective)
		}
	}
	ctx.Flash(fmt.Sprintf("queued goal %q not found", id))
	return ""
}

// promptCreateForPlacement opens the create-goal flow for the manager's add
// rows: with an active goal, placementNext prepends to the queue (the goal
// runs next) and placementLast appends — neither silently replaces the
// running goal (replacement goes through /goal:replace). With no active
// goal, the new goal starts immediately regardless of placement.
func (c *GoalCommand) promptCreateForPlacement(ctx core.Context, placement goalPlacement) {
	if c.Mode.GetGoal().Goal == nil {
		_ = c.promptCreateInteractive(ctx, placementAsk, c.resolveFresh(""))
		return
	}
	_ = c.promptCreateInteractive(ctx, placement, c.resolveFresh(""))
}

// promptEditQueuedGoal implements the 'e' hotkey of /goal:manage: it opens an
// input prompt pre-filled with the queued goal's current objective and
// persists the edit via Queue.Update. The manager reopens afterwards —
// including on cancel or an empty submission — so it stays open until the
// user picks Done.
func (c *GoalCommand) promptEditQueuedGoal(ctx core.Context, id string) {
	goals, err := c.Queue.Read()
	if err != nil {
		ctx.Flash(err.Error())
		return
	}
	idx := -1
	for i := range goals {
		if goals[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		ctx.Flash(fmt.Sprintf("queued goal %q not found", id))
		_ = c.showQueueManagerAt(ctx, "")
		return
	}
	current := goals[idx].Objective
	ctx.ShowInput("Edit goal description:", current, func(value string, ok bool) {
		c.applyEditedObjective(ctx, id, current, value, ok)
		_ = c.showQueueManagerAt(ctx, id)
	})
}

// applyEditedObjective persists a submitted edit: a non-empty, changed
// objective is written via Queue.Update; cancel, blank, or unchanged input
// is a no-op.
func (c *GoalCommand) applyEditedObjective(ctx core.Context, id, current, value string, ok bool) {
	if !ok {
		return
	}
	v := strings.TrimSpace(value)
	if v == "" || v == current {
		return
	}
	if _, err := c.Queue.Update(id, v); err != nil {
		ctx.Flash(err.Error())
		return
	}
	c.notifyQueueChanged()
	ctx.Flash("Goal updated.")
}

func (c *GoalCommand) reorderQueue(ctx core.Context, mapping string) error {
	goals, err := c.Queue.ReorderByMapping(mapping)
	if err != nil {
		return err
	}
	c.notifyQueueChanged()
	writeStr(ctx, "Queue reordered:\n")
	for i, g := range goals {
		name := g.Name
		if name == "" {
			name = "(unnamed)"
		}
		writeFmt(ctx, "%d. %s — %s\n", i+1, name, truncate(g.Objective, 60))
	}
	return nil
}

// goalPlacement describes where a newly-created goal should go.
type goalPlacement int

const (
	// placementAsk prompts the user (first/active vs last/queue) — used when a
	// goal is already active. Equivalent to the item-4 "1st or last" prompt.
	placementAsk goalPlacement = iota
	// placementFirst replaces the active goal (becomes first).
	placementFirst
	// placementNext inserts at the FRONT of the queue (/goal:next[:first]):
	// the goal is promoted right after the active goal completes. Unlike
	// placementFirst it never touches the active goal.
	placementNext
	// placementLast appends to the END of the queue (/goal:next:last).
	placementLast
)

// create handles /goal:new:<text> and bare /goal:<text>.
// When a goal is already active, it asks whether to become first (replace) or
// last (queue) — the item-4 prompt. fresh is the resolved context mode
// (/goal:new:fresh|reuse or the configured default).
