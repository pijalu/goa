// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
)

// ProfileCommand is a compatibility alias for /mode.
type ProfileCommand struct{ ModeCommand }

func (c *ProfileCommand) Name() string      { return "profile" }
func (c *ProfileCommand) Aliases() []string { return []string{} }
func (c *ProfileCommand) ShortHelp() string { return "Alias for /mode" }
func (c *ProfileCommand) LongHelp() string  { return help.LongHelp(c.Name()) }

// Status implements core.StatusProvider so /profile? prints the live value.
func (c *ProfileCommand) Status(ctx core.Context) string {
	if ctx.Config == nil {
		return ""
	}
	return "Mode: " + ctx.Config.ActiveMajor()
}

func (c *ProfileCommand) Run(ctx core.Context, args []string) error {
	return c.ModeCommand.Run(ctx, args)
}
