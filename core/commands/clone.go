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

// CloneCommand handles /clone for cloning a session branch.
type CloneCommand struct {
	Manager *sessiontree.Manager
}

// Name returns the command name.
func (c *CloneCommand) Name() string { return "clone" }

// Aliases returns command aliases.
func (c *CloneCommand) Aliases() []string { return nil }

// ShortHelp returns a short description.
func (c *CloneCommand) ShortHelp() string { return "Clone a session branch" }

// LongHelp returns usage help.
func (c *CloneCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs provides node ID completions from the session tree.
func (c *CloneCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	if c.Manager == nil {
		return nil
	}
	return nodeCompletions(c.Manager.Tree(), prefix)
}

// Run executes the command.
func (c *CloneCommand) Run(ctx core.Context, args []string) error {
	if c.Manager == nil {
		return fmt.Errorf("session tree not configured")
	}
	if len(args) == 0 {
		return fmt.Errorf("source node ID required: /clone:<source-node-id>")
	}
	parentID := c.Manager.Tree().Root().ID
	n, err := c.Manager.Clone(args[0], parentID)
	if err != nil {
		return fmt.Errorf("clone failed: %w", err)
	}
	ctx.Writef("Cloned branch %s\n", n.ID)
	return nil
}
