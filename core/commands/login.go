// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	oauth "github.com/pijalu/goa/internal/agentic/provider/oauth"
	"github.com/pijalu/goa/internal/auth"
)

// LoginCommand handles /login for managing OAuth tokens.
type LoginCommand struct {
	Store *auth.Store
}

// Name returns the command name.
func (c *LoginCommand) Name() string { return "login" }

// Aliases returns command aliases.
func (c *LoginCommand) Aliases() []string { return nil }

// ShortHelp returns a short description.
func (c *LoginCommand) ShortHelp() string { return "Manage OAuth logins for providers" }

// LongHelp returns usage help.
func (c *LoginCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs provides argument completions for providers.
func (c *LoginCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	providers := []string{"github"}
	var comps []core.ArgCompletion
	for _, p := range providers {
		if prefix == "" || strings.HasPrefix(p, prefix) {
			comps = append(comps, core.ArgCompletion{Value: p, Description: "OAuth provider"})
		}
	}
	return comps
}

// Run executes the login command.
func (c *LoginCommand) Run(ctx core.Context, args []string) error {
	if c.Store == nil {
		return fmt.Errorf("auth store not configured")
	}
	if len(args) == 0 {
		return c.listProviders(ctx)
	}
	provider := args[0]
	if len(args) >= 2 {
		token := args[1]
		tokens := &oauth.Tokens{AccessToken: token}
		if err := c.Store.Set(provider, tokens); err != nil {
			return fmt.Errorf("failed to store token: %w", err)
		}
		ctx.Writef("Stored token for %s\n", provider)
		return nil
	}
	ctx.Writef("OAuth login for %s: open the provider's authorization page and paste the token with /login:%s:<token>.\n", provider, provider)
	return nil
}

func (c *LoginCommand) listProviders(ctx core.Context) error {
	providers := c.Store.Providers()
	if len(providers) == 0 {
		ctx.Writef("No OAuth tokens stored.\n")
		return nil
	}
	ctx.Writef("Stored OAuth providers:\n")
	for _, p := range providers {
		ctx.Writef("  %s\n", p)
	}
	return nil
}
