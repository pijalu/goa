// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/core/sessiontree"
)

// ForkCommand handles /fork for creating a new session branch.
type ForkCommand struct {
	Manager *sessiontree.Manager
}

// Name returns the command name.
func (c *ForkCommand) Name() string { return "fork" }

// Aliases returns command aliases.
func (c *ForkCommand) Aliases() []string { return nil }

// ShortHelp returns a short description.
func (c *ForkCommand) ShortHelp() string { return "Create a new session branch" }

// LongHelp returns usage help.
func (c *ForkCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs provides node ID completions from the session tree.
func (c *ForkCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	if c.Manager == nil {
		return nil
	}
	return nodeCompletions(c.Manager.Tree(), prefix)
}

// Run executes the command.
func (c *ForkCommand) Run(ctx core.Context, args []string) error {
	if c.Manager == nil {
		return fmt.Errorf("session tree not configured")
	}
	if len(args) == 0 {
		return fmt.Errorf("parent node ID required: /fork:<parent-node-id>")
	}
	n, err := c.Manager.Fork(args[0], "", "")
	if err != nil {
		return fmt.Errorf("fork failed: %w", err)
	}
	ctx.Writef("Created branch %s\n", n.ID)
	return nil
}

func nodeCompletions(t *sessiontree.Tree, prefix string) []core.ArgCompletion {
	if t == nil {
		return nil
	}
	var comps []core.ArgCompletion
	for _, n := range t.All() {
		if prefix == "" || (len(prefix) <= len(n.ID) && n.ID[:len(prefix)] == prefix) {
			comps = append(comps, core.ArgCompletion{Value: n.ID, Description: n.Summary})
		}
	}
	return comps
}
