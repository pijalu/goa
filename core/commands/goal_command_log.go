// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/tui"
)

func (c *GoalCommand) showEventLog(ctx core.Context) error {
	records, err := c.Mode.EventLog()
	if err != nil {
		return err
	}
	if len(records) == 0 {
		writeStr(ctx, "No goal events recorded.\n")
		return nil
	}
	if len(records) > goalLogLimit {
		writeFmt(ctx, "(showing last %d of %d records)\n", goalLogLimit, len(records))
		records = records[len(records)-goalLogLimit:]
	}
	for _, r := range records {
		writeStr(ctx, formatGoalEventRecord(r))
	}
	return nil
}

// formatGoalEventRecord renders one event-log record as a single line,
// appending optional fields only when present.
func formatGoalEventRecord(r goal.GoalEventRecord) string {
	var b strings.Builder
	b.WriteString(r.Timestamp.Format("15:04:05"))
	b.WriteString("  ")
	b.WriteString(string(r.Type))
	if r.Actor != nil {
		b.WriteString("  actor=")
		b.WriteString(*r.Actor)
	}
	if r.Status != nil {
		b.WriteString("  status=")
		b.WriteString(*r.Status)
	}
	if r.Name != nil {
		b.WriteString("  name=")
		b.WriteString(*r.Name)
	}
	if r.Reason != nil {
		b.WriteString("  reason=")
		b.WriteString(truncate(*r.Reason, 60))
	}
	if r.Expectation != nil {
		b.WriteString("  expectation=")
		b.WriteString(truncate(*r.Expectation, 60))
	}
	b.WriteByte('\n')
	return b.String()
}

// runVerify implements /goal:verify: execute the current goal's recorded
// verify command on demand and print its output plus PASS/FAIL.
func (c *GoalCommand) runVerify(ctx core.Context) error {
	output, ok, err := c.Mode.RunVerifyCommand(context.Background())
	if err != nil {
		return err
	}
	if output != "" {
		writeStr(ctx, output)
		if !strings.HasSuffix(output, "\n") {
			writeStr(ctx, "\n")
		}
	}
	if ok {
		writeStr(ctx, "verify command: PASS\n")
	} else {
		writeStr(ctx, "verify command: FAIL\n")
	}
	return nil
}

// openSettings implements /goal:settings — a selector mirroring the
// /config → Goals menu, so both entry points expose the same toggles with the
// same UX. Currently: auto-unblock on/off.
func (c *GoalCommand) openSettings(ctx core.Context) error {
	enabled := ctx.Config.Goals.AutoUnblockEnabled()
	items := []tui.SelectorItem{
		{Value: "auto_unblock", Label: "Auto-unblock goals", Description: boolLabel(enabled)},
	}
	ctx.SelectOption("Goal settings:", items, "", func(field string, ok bool) {
		if !ok || field != "auto_unblock" {
			return
		}
		g := &ctx.Config.Goals
		next := !g.AutoUnblockEnabled()
		g.AutoUnblock = &next
		if ctx.ConfigSaver != nil {
			if err := ctx.ConfigSaver.Save(ctx.Config); err != nil {
				ctx.Flash("Failed to save config: " + err.Error())
				return
			}
		}
		ctx.Flash("Auto-unblock goals " + goalOnOffLabel(next))
	})
	return nil
}

// goalOnOffLabel renders an on/off state for flash messages (matches the
// config menu's toggle feedback).
func goalOnOffLabel(v bool) string {
	if v {
		return "enabled"
	}
	return "disabled"
}

// notifyQueueChanged pushes a fresh goal snapshot to the footer/bubble after
// a durable-queue mutation. Queue operations emit no goal lifecycle events on
// their own, so without this the footer's ◈ count (1 active + queued) stays
// stale until the next active-goal event. The published update carries no
// Change, so no chat marker is emitted.
func (c *GoalCommand) notifyQueueChanged() {
	if c.Mode != nil {
		c.Mode.NotifyGoalChanged()
	}
}

// queueNext inserts a goal at the FRONT of the durable queue (/goal:next and
// /goal:next:first): it is promoted NEXT, right after the active goal
// completes. fresh is the resolved context mode — stored with the goal so
// its turns run on a clean context (or the surviving conversation) when it
// is promoted.
