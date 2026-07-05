// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/tui"
)

// Ensure tui is used
var _ = tui.SelectorItem{}

// AutonomyCommand sets or displays the autonomy level.
type AutonomyCommand struct{}

func (c *AutonomyCommand) Name() string      { return "autonomy" }
func (c *AutonomyCommand) Aliases() []string { return []string{} }
func (c *AutonomyCommand) ShortHelp() string { return "Set or display the autonomy level" }
func (c *AutonomyCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	current := ""
	if mode := ctx.CurrentMode(); !mode.IsZero() {
		current = string(mode.Autonomy)
	}
	var comps []core.ArgCompletion
	for _, v := range []struct{ val, desc string }{
		{"yolo", "execute all tool calls automatically"},
		{"solo", "auto-run tools constrained to the codebase"},
		{"confirm", "pause before each tool call"},
		{"review", "queue writes for batch approval"},
	} {
		if v.val == current {
			continue
		}
		if strings.HasPrefix(v.val, prefix) {
			comps = append(comps, core.ArgCompletion{Value: v.val, Description: v.desc})
		}
	}
	return comps
}
func (c *AutonomyCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *AutonomyCommand) Run(ctx core.Context, args []string) error {
	if len(args) == 0 {
		return showAutonomyPicker(ctx)
	}

	if ctx.CurrentMode().IsZero() {
		return fmt.Errorf("no active agent session")
	}

	autonomy := internal.AutonomyLevel(strings.ToLower(args[0]))
	switch autonomy {
	case internal.AutonomyYolo, internal.AutonomySolo, internal.AutonomyConfirm, internal.AutonomyReview:
		if err := setAutonomyAndPersist(ctx, ctx.CurrentMode(), autonomy); err != nil {
			return err
		}
		writeFmt(ctx, "Switched to %s autonomy\n", autonomy)
	default:
		return fmt.Errorf("unknown autonomy: %s (use yolo, solo, confirm, or review)", args[0])
	}
	return nil
}

func setAutonomyAndPersist(ctx core.Context, current internal.ModeState, autonomy internal.AutonomyLevel) error {
	ctx.SetMode(current.WithAutonomy(autonomy))
	if ctx.Config != nil {
		if ctx.Config.Mode.Defaults == nil {
			ctx.Config.Mode.Defaults = make(map[internal.MajorMode]internal.AutonomyLevel)
		}
		ctx.Config.Mode.Defaults[current.Major] = autonomy
	}
	if err := saveProjectConfig(ctx.Config, ctx.ConfigSaver); err != nil {
		return err
	}
	return nil
}

func showAutonomyPicker(ctx core.Context) error {
	current := ctx.CurrentMode()
	if current.IsZero() {
		writeStr(ctx, "No active session. Start one first.\n")
		return nil
	}
	currentStr := string(current.Autonomy)

	items := []tui.SelectorItem{
		{Value: "yolo", Label: "yolo", Description: "execute all tool calls automatically"},
		{Value: "solo", Label: "solo", Description: "auto-run tools constrained to the codebase"},
		{Value: "confirm", Label: "confirm", Description: "pause before each tool call"},
		{Value: "review", Label: "review", Description: "queue writes for batch approval"},
	}

	writeStr(ctx, "Opening autonomy selector...\n")
	ctx.SelectOption("Select autonomy level:", items, currentStr, func(selected string, ok bool) {
		if !ok || selected == currentStr {
			return
		}
		autonomy := internal.AutonomyLevel(selected)
		if err := setAutonomyAndPersist(ctx, current, autonomy); err != nil {
			ctx.Flash(err.Error())
			return
		}
		ctx.Flash("Switched to " + selected + " autonomy")
	})
	return nil
}
