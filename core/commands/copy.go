// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal"
)

// CopyCommand copies the last assistant message to the clipboard.
type CopyCommand struct{}

func (c *CopyCommand) Name() string      { return "copy" }
func (c *CopyCommand) IsInternal() bool  { return true }
func (c *CopyCommand) Aliases() []string { return nil }
func (c *CopyCommand) ShortHelp() string { return "Copy the last assistant message to the clipboard" }
func (c *CopyCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}
func (c *CopyCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion { return nil }

func (c *CopyCommand) Run(ctx core.Context, args []string) error {
	text := ctx.AssistantText
	if text == "" {
		ctx.Flash("No agent messages to copy yet.")
		return nil
	}

	if err := internal.CopyToClipboard(text); err != nil {
		ctx.Flash(fmt.Sprintf("Copy failed: %v", err))
		return nil
	}

	ctx.Flash("Copied last agent message to clipboard.")
	return nil
}
