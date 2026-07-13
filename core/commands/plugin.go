// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/plugins"
)

// PluginCommand manages Goa plugins: install, list, remove, enable, disable.
type PluginCommand struct {
	Manager *plugins.Manager
}

// Name returns the command name.
func (c *PluginCommand) Name() string { return "plugin" }

// Aliases returns command aliases.
func (c *PluginCommand) Aliases() []string { return []string{"plugins"} }

// ShortHelp returns a short description.
func (c *PluginCommand) ShortHelp() string { return "Manage plugins" }

// LongHelp returns usage help.
func (c *PluginCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs provides argument completions.
func (c *PluginCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	return nil
}

// Run executes the plugin command.
func (c *PluginCommand) Run(ctx core.Context, args []string) error {
	if c.Manager == nil {
		return fmt.Errorf("plugin manager not configured")
	}
	if len(args) == 0 {
		return c.list(ctx)
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "install":
		if len(args) < 2 {
			return fmt.Errorf("usage: /plugin install <git-url>")
		}
		return c.install(ctx, args[1])
	case "remove", "uninstall":
		if len(args) < 2 {
			return fmt.Errorf("usage: /plugin remove <id>")
		}
		return c.remove(ctx, args[1])
	case "enable":
		if len(args) < 2 {
			return fmt.Errorf("usage: /plugin enable <id>")
		}
		return c.enable(ctx, args[1])
	case "disable":
		if len(args) < 2 {
			return fmt.Errorf("usage: /plugin disable <id>")
		}
		return c.disable(ctx, args[1])
	case "list", "ls":
		return c.list(ctx)
	default:
		return fmt.Errorf("unknown plugin subcommand %q", sub)
	}
}

func (c *PluginCommand) install(ctx core.Context, source string) error {
	id, err := c.Manager.Install(source)
	if err != nil {
		return err
	}
	ctx.Writef("Installed plugin %s. Run /plugin enable %s to activate it.\n", id, id)
	return nil
}

func (c *PluginCommand) remove(ctx core.Context, id string) error {
	if err := c.Manager.Remove(id); err != nil {
		return err
	}
	ctx.Writef("Removed plugin %s.\n", id)
	return nil
}

func (c *PluginCommand) enable(ctx core.Context, id string) error {
	if err := c.Manager.Enable(id); err != nil {
		return err
	}
	ctx.Writef("Enabled plugin %s.\n", id)
	return nil
}

func (c *PluginCommand) disable(ctx core.Context, id string) error {
	if err := c.Manager.Disable(id); err != nil {
		return err
	}
	ctx.Writef("Disabled plugin %s.\n", id)
	return nil
}

func (c *PluginCommand) list(ctx core.Context) error {
	entries := c.Manager.List()
	if len(entries) == 0 {
		ctx.Writef("No plugins installed.\n")
		return nil
	}
	ctx.Writef("Installed plugins:\n")
	for _, e := range entries {
		status := "disabled"
		if e.Enabled {
			status = "enabled"
		}
		ctx.Writef("  %s (%s, hash %s)\n", e.ID, status, shortHash(e.Hash))
	}
	return nil
}

func shortHash(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}
