// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"context"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal/update"
)

// UpdateCommand handles /update for checking new releases.
type UpdateCommand struct {
	Checker *update.Checker
}

// Name returns the command name.
func (c *UpdateCommand) Name() string { return "update" }

// Aliases returns command aliases.
func (c *UpdateCommand) Aliases() []string { return nil }

// ShortHelp returns a short description.
func (c *UpdateCommand) ShortHelp() string { return "Check for updates" }

// LongHelp returns usage help.
func (c *UpdateCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs returns no args.
func (c *UpdateCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	return nil
}

// Run executes the command.
func (c *UpdateCommand) Run(ctx core.Context, args []string) error {
	if c.Checker == nil {
		return nil
	}
	res, err := c.Checker.Check(context.Background())
	if err != nil {
		ctx.Writef("Update check failed: %v\n", err)
		return nil
	}
	if c.Checker.IsNewer(res.LatestVersion) {
		ctx.Writef("A newer version is available: %s\n%s\n", res.LatestVersion, res.URL)
	} else {
		ctx.Writef("You are on the latest version (%s).\n", c.Checker.CurrentVersion)
	}
	return nil
}
