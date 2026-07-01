// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal/auth"
)

// LogoutCommand handles /logout for removing OAuth tokens.
type LogoutCommand struct {
	Store *auth.Store
}

// Name returns the command name.
func (c *LogoutCommand) Name() string { return "logout" }

// Aliases returns command aliases.
func (c *LogoutCommand) Aliases() []string { return nil }

// ShortHelp returns a short description.
func (c *LogoutCommand) ShortHelp() string { return "Remove stored OAuth tokens" }

// LongHelp returns usage help.
func (c *LogoutCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs provides argument completions for stored providers.
func (c *LogoutCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	if c.Store == nil {
		return nil
	}
	for _, p := range c.Store.Providers() {
		if prefix == "" || strings.HasPrefix(p, prefix) {
			return []core.ArgCompletion{{Value: p, Description: "Remove stored token"}}
		}
	}
	return nil
}

// Run executes the logout command.
func (c *LogoutCommand) Run(ctx core.Context, args []string) error {
	if c.Store == nil {
		return fmt.Errorf("auth store not configured")
	}
	if len(args) == 0 {
		return fmt.Errorf("provider required: /logout:<provider>")
	}
	if err := c.Store.Delete(args[0]); err != nil {
		return fmt.Errorf("failed to remove token: %w", err)
	}
	ctx.Writef("Logged out of %s\n", args[0])
	return nil
}
