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

// TreeCommand handles /tree for listing the session tree.
type TreeCommand struct {
	Manager *sessiontree.Manager
}

// Name returns the command name.
func (c *TreeCommand) Name() string { return "tree" }

// Aliases returns command aliases.
func (c *TreeCommand) Aliases() []string { return nil }

// ShortHelp returns a short description.
func (c *TreeCommand) ShortHelp() string { return "Manage the session tree" }

// LongHelp returns usage help.
func (c *TreeCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs returns no args.
func (c *TreeCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion { return nil }

// Run executes the command.
func (c *TreeCommand) Run(ctx core.Context, args []string) error {
	if c.Manager == nil {
		return fmt.Errorf("session tree not configured")
	}
	root := c.Manager.Tree().Root()
	if root == nil {
		ctx.Writef("No session tree.\n")
		return nil
	}
	printTree(ctx, c.Manager.Tree(), root, "")
	return nil
}

func printTree(ctx core.Context, t *sessiontree.Tree, n *sessiontree.Node, indent string) {
	ctx.Writef("%s%s: %s\n", indent, n.ID, n.Summary)
	for _, child := range t.Children(n.ID) {
		printTree(ctx, t, child, indent+"  ")
	}
}
