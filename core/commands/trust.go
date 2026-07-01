// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"sort"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal/trust"
)

// TrustCommand handles /trust list|allow|deny|prompt.
type TrustCommand struct {
	Manager *trust.Manager
}

// Name returns the command name.
func (c *TrustCommand) Name() string { return "trust" }

// Aliases returns aliases.
func (c *TrustCommand) Aliases() []string { return nil }

// ShortHelp returns a short description.
func (c *TrustCommand) ShortHelp() string { return "Manage project trust decisions" }

// LongHelp returns usage help.
func (c *TrustCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs provides argument completions for trust subcommands.
func (c *TrustCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	parts := strings.SplitN(prefix, ":", 2)
	if len(parts) == 1 {
		return trustSubcompletions(prefix)
	}
	if len(parts) == 2 {
		return trustArgCompletions(c.Manager, parts[0], parts[1])
	}
	return nil
}

func trustSubcompletions(prefix string) []core.ArgCompletion {
	subs := []string{"list", "allow", "deny", "prompt"}
	var comps []core.ArgCompletion
	for _, s := range subs {
		if prefix == "" || strings.HasPrefix(s, prefix) {
			comps = append(comps, core.ArgCompletion{Value: s})
		}
	}
	return comps
}

func trustArgCompletions(mgr *trust.Manager, sub, argPrefix string) []core.ArgCompletion {
	switch sub {
	case "allow", "deny", "prompt":
		if mgr != nil {
			var comps []core.ArgCompletion
			for _, d := range mgr.Domains() {
				if argPrefix == "" || strings.HasPrefix(d, argPrefix) {
					comps = append(comps, core.ArgCompletion{Value: d})
				}
			}
			return comps
		}
	}
	return nil
}

// Run executes the command.
func (c *TrustCommand) Run(ctx core.Context, args []string) error {
	if c.Manager == nil {
		return fmt.Errorf("trust manager not configured")
	}
	if len(args) == 0 {
		return c.listTrust(ctx)
	}
	switch args[0] {
	case "list":
		return c.listTrust(ctx)
	case "allow", "trust":
		return c.setTrust(ctx, args[1:], trust.DecisionTrusted)
	case "deny", "untrust":
		return c.setTrust(ctx, args[1:], trust.DecisionUntrusted)
	case "prompt":
		return c.setTrust(ctx, args[1:], trust.DecisionPrompt)
	default:
		return fmt.Errorf("unknown trust subcommand: %s (try list, allow, deny, prompt)", args[0])
	}
}

func (c *TrustCommand) listTrust(ctx core.Context) error {
	domains := c.Manager.Domains()
	sort.Strings(domains)
	if len(domains) == 0 {
		ctx.Writef("No trust decisions stored.\n")
		return nil
	}
	ctx.Writef("Trust decisions:\n")
	for _, d := range domains {
		ctx.Writef("  %s: %s\n", d, c.Manager.Decision(d))
	}
	return nil
}

func (c *TrustCommand) setTrust(ctx core.Context, args []string, decision trust.Decision) error {
	if len(args) == 0 {
		return fmt.Errorf("domain required: /trust:%s:<domain>", decision)
	}
	domain := strings.Join(args, " ")
	switch decision {
	case trust.DecisionTrusted:
		if err := c.Manager.Trust(domain); err != nil {
			return err
		}
		ctx.Writef("Trusted %s\n", domain)
	case trust.DecisionUntrusted:
		if err := c.Manager.Untrust(domain); err != nil {
			return err
		}
		ctx.Writef("Denied %s\n", domain)
	case trust.DecisionPrompt:
		if err := c.Manager.Prompt(domain); err != nil {
			return err
		}
		ctx.Writef("Prompt for %s\n", domain)
	}
	return nil
}
