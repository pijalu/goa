// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/tui"
)

// ModeCommand manages the agent's mode.
type ModeCommand struct{}

func (c *ModeCommand) Name() string      { return "mode" }
func (c *ModeCommand) Aliases() []string { return []string{} }
func (c *ModeCommand) ShortHelp() string { return "Set or display the agent's mode" }

// CompleteArgs returns argument completions for /mode.
func (c *ModeCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, m := range majorModeChoices(ctx.ModeRegistry) {
		if strings.HasPrefix(m, prefix) {
			comps = append(comps, core.ArgCompletion{Value: m, Description: modeDescription(ctx.ModeRegistry, m)})
		}
	}
	return comps
}
func (c *ModeCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *ModeCommand) Run(ctx core.Context, args []string) error {
	if len(args) == 0 {
		return showModePicker(ctx)
	}
	return switchMajor(ctx, args[0])
}

func showCurrentMode(ctx core.Context) error {
	if ctx.AgentManager == nil {
		writeStr(ctx, "Current mode: unavailable (no active session)\n")
		return nil
	}
	current := ctx.CurrentMode()
	writeFmt(ctx, "Current mode: %s\n", current.String())
	return nil
}

func showModePicker(ctx core.Context) error {
	current := ctx.CurrentMode()
	if current.IsZero() {
		writeStr(ctx, "No active session. Start one first.\n")
		return nil
	}
	currentMajor := string(current.Major)

	items := make([]tui.SelectorItem, 0, len(majorModeChoices(ctx.ModeRegistry)))
	for _, m := range majorModeChoices(ctx.ModeRegistry) {
		items = append(items, tui.SelectorItem{
			Value:       m,
			Label:       m,
			Description: modeDescription(ctx.ModeRegistry, m),
		})
	}

	if len(items) == 0 {
		return showCurrentMode(ctx)
	}

	sort.Slice(items, func(i, j int) bool {
		return strings.ToLower(items[i].Label) < strings.ToLower(items[j].Label)
	})

	writeStr(ctx, "Opening mode selector...\n")
	ctx.SelectOption("Select mode:", items, currentMajor, func(selected string, ok bool) {
		if !ok || selected == currentMajor {
			return
		}
		_ = switchMajor(ctx, selected)
	})
	return nil
}

func modeDescription(reg *core.ModeRegistry, m string) string {
	if reg == nil {
		return string(config.DefaultAutonomyForMajor(internal.MajorMode(m)))
	}
	spec, err := reg.Resolve(internal.MajorMode(m))
	if err != nil {
		return string(config.DefaultAutonomyForMajor(internal.MajorMode(m)))
	}
	if spec.Description != "" {
		return spec.Description
	}
	return string(spec.DefaultAutonomy)
}

func switchMajor(ctx core.Context, name string) error {
	major := internal.MajorMode(name)

	if ctx.ModeRegistry != nil {
		if _, err := ctx.ModeRegistry.Resolve(major); err != nil {
			return fmt.Errorf("unknown major: %q", major)
		}
	}

	if ctx.CurrentMode().IsZero() {
		return fmt.Errorf("no active agent session")
	}

	var newMode internal.ModeState
	if ctx.ModeRegistry != nil {
		newMode = ctx.ModeRegistry.DefaultForMajor(major)
	} else {
		newMode = internal.ModeState{Major: major}
	}

	ctx.SetMode(newMode)
	if err := persistMajorMode(ctx, newMode); err != nil {
		return err
	}

	writeFmt(ctx, "Switched to %s\n", major)
	return nil
}

func persistMajorMode(ctx core.Context, newMode internal.ModeState) error {
	if ctx.Config == nil {
		return nil
	}
	ctx.Config.Mode.Default.Major = newMode.Major
	return saveProjectConfig(ctx.Config, ctx.ConfigSaver)
}

func majorModeChoices(reg *core.ModeRegistry) []string {
	var out []string
	if reg != nil {
		for _, m := range reg.Majors() {
			out = append(out, string(m))
		}
	}
	return out
}
