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
	"github.com/pijalu/goa/tui"
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

// CompleteArgs provides argument completions using goa's colon syntax:
// /plugin:<sub>[:<id>]. Subcommand names complete after the first colon;
// plugin IDs complete after /plugin:enable:, /plugin:disable:, /plugin:remove:.
func (c *PluginCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	if c.Manager == nil {
		return nil
	}
	arg := strings.TrimPrefix(prefix, ":")
	// Second colon → complete a plugin id for the given subcommand. The
	// completion engine prepends "/plugin:" to each Value, so the id Value
	// must itself carry the subcommand (e.g. "disable:provider-quota") or the
	// result collapses to "/plugin:<id>".
	if idx := strings.Index(arg, ":"); idx >= 0 {
		sub := strings.ToLower(arg[:idx])
		idPrefix := arg[idx+1:]
		switch sub {
		case "enable":
			return prefixedIDs(sub, completePluginIDs(c.Manager, idPrefix, boolPtr(false)))
		case "disable":
			return prefixedIDs(sub, completePluginIDs(c.Manager, idPrefix, boolPtr(true)))
		case "remove", "uninstall":
			return prefixedIDs(sub, completePluginIDs(c.Manager, idPrefix, nil))
		}
		return nil
	}
	// First colon (or none yet) → complete the subcommand name.
	subs := []core.ArgCompletion{
		{Value: "list", Description: "List installed plugins and their state"},
		{Value: "enable", Description: "Enable an installed plugin"},
		{Value: "disable", Description: "Disable an enabled plugin"},
		{Value: "install", Description: "Install a plugin from a git URL"},
		{Value: "remove", Description: "Remove an installed plugin"},
	}
	if arg == "" {
		return subs
	}
	var out []core.ArgCompletion
	for _, s := range subs {
		if strings.HasPrefix(s.Value, arg) {
			out = append(out, s)
		}
	}
	return out
}

// completePluginIDs returns completion candidates for installed plugin IDs.
// When wantEnabled is non-nil, only plugins in that enabled state are offered:
// /plugin enable completes only disabled plugins, /plugin disable only enabled
// ones; remove/uninstall pass nil to offer all.
func completePluginIDs(m *plugins.Manager, prefix string, wantEnabled *bool) []core.ArgCompletion {
	var out []core.ArgCompletion
	for _, e := range m.List() {
		if wantEnabled != nil && e.Enabled != *wantEnabled {
			continue
		}
		if prefix == "" || strings.HasPrefix(e.ID, prefix) {
			state := "disabled"
			if e.Enabled {
				state = "enabled"
			}
			out = append(out, core.ArgCompletion{Value: e.ID, Description: state})
		}
	}
	return out
}

// boolPtr is a tiny helper for optional bool filters.
func boolPtr(b bool) *bool { return &b }

// Run executes the plugin command.
func (c *PluginCommand) Run(ctx core.Context, args []string) error {
	if c.Manager == nil {
		return fmt.Errorf("plugin manager not configured")
	}
	if len(args) == 0 {
		return c.interactive(ctx)
	}

	sub := strings.ToLower(args[0])
	switch sub {
	case "install":
		if len(args) < 2 {
			return fmt.Errorf("usage: /plugin:install:<git-url>")
		}
		return c.install(ctx, args[1])
	case "remove", "uninstall":
		if len(args) < 2 {
			return fmt.Errorf("usage: /plugin:remove:<id>")
		}
		return c.remove(ctx, args[1])
	case "enable":
		if len(args) < 2 {
			return fmt.Errorf("usage: /plugin:enable:<id>")
		}
		return c.enable(ctx, args[1])
	case "disable":
		if len(args) < 2 {
			return fmt.Errorf("usage: /plugin:disable:<id>")
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
	ctx.Writef("Installed plugin %s. Run /plugin:enable:%s to activate it.\n", id, id)
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
		hint := "  → /plugin:enable:" + e.ID
		if e.Enabled {
			status = "enabled"
			hint = "  → /plugin:disable:" + e.ID
		}
		ctx.Writef("  %s (%s, hash %s)%s\n", e.ID, status, shortHash(e.Hash), hint)
	}
	return nil
}

func shortHash(h string) string {
	if len(h) <= 8 {
		return h
	}
	return h[:8]
}

// interactive opens a selector listing installed plugins so the user can
// toggle each one enabled/disabled — mirroring /config → Tools. Selecting a
// row flips that plugin's state (persisted to the lockfile via
// Manager.Enable/Disable) and re-opens the selector; Esc closes it.
func (c *PluginCommand) interactive(ctx core.Context) error {
	entries := c.Manager.List()
	if len(entries) == 0 {
		ctx.Writef("No plugins installed.\n")
		return nil
	}
	ctx.SelectOption("Plugins (enter to toggle):", c.selectorItems(), "", func(selected string, ok bool) {
		if !ok {
			return
		}
		c.toggle(ctx, selected)
	})
	return nil
}

// selectorItems builds one selector row per installed plugin: the value is the
// plugin id, the label its name, and the description its current state
// (on/off, mirroring /config → Tools) plus the integrity hash.
func (c *PluginCommand) selectorItems() []tui.SelectorItem {
	entries := c.Manager.List()
	items := make([]tui.SelectorItem, 0, len(entries))
	for _, e := range entries {
		items = append(items, tui.SelectorItem{
			Value:       e.ID,
			Label:       e.ID,
			Description: boolLabel(e.Enabled) + " · " + shortHash(e.Hash),
		})
	}
	return items
}

// toggle flips a plugin's enabled state, persists it, and re-opens the
// selector so the user can keep toggling.
func (c *PluginCommand) toggle(ctx core.Context, id string) {
	if id == "" {
		return
	}
	var err error
	if c.Manager.IsEnabled(id) {
		err = c.Manager.Disable(id)
	} else {
		err = c.Manager.Enable(id)
	}
	if err != nil {
		ctx.Flash("Plugin " + id + ": " + err.Error())
	} else {
		ctx.Flash("Plugin " + id + " " + toggleNextLabel(c.Manager.IsEnabled(id)))
	}
	// Re-open the selector with fresh state.
	_ = c.interactive(ctx)
}
