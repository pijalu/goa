// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
)

// ThemeCommand handles /theme for managing UI themes.
type ThemeCommand struct {
	Store *config.ThemeStore
}

// Name returns the command name.
func (c *ThemeCommand) Name() string { return "theme" }

// Aliases returns command aliases.
func (c *ThemeCommand) Aliases() []string { return nil }

// ShortHelp returns a short description.
func (c *ThemeCommand) ShortHelp() string { return "Manage UI themes" }

// LongHelp returns usage help.
func (c *ThemeCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs provides argument completions for theme subcommands and names.
func (c *ThemeCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	parts := strings.SplitN(prefix, ":", 2)
	var comps []core.ArgCompletion

	switch len(parts) {
	case 1:
		subs := []string{"list", "set", "edit"}
		for _, s := range subs {
			if prefix == "" || strings.HasPrefix(s, prefix) {
				comps = append(comps, core.ArgCompletion{Value: s, Description: "Theme subcommand"})
			}
		}
	case 2:
		sub := parts[0]
		argPrefix := parts[1]
		switch sub {
		case "set", "edit":
			return themeNameCompletions(c.Store, argPrefix)
		}
	}
	return comps
}

func themeNameCompletions(store *config.ThemeStore, prefix string) []core.ArgCompletion {
	if store == nil {
		return nil
	}
	names, err := store.List()
	if err != nil {
		return nil
	}
	var comps []core.ArgCompletion
	for _, n := range names {
		if prefix == "" || strings.HasPrefix(n, prefix) {
			comps = append(comps, core.ArgCompletion{Value: n, Description: "Theme name"})
		}
	}
	return comps
}

// Run executes the command.
func (c *ThemeCommand) Run(ctx core.Context, args []string) error {
	if c.Store == nil {
		return fmt.Errorf("theme store not configured")
	}
	if len(args) == 0 {
		return c.listThemes(ctx)
	}
	switch args[0] {
	case "list", "ls":
		return c.listThemes(ctx)
	case "set":
		if len(args) < 2 {
			return fmt.Errorf("theme name required: /theme:set:<name>")
		}
		return c.setTheme(ctx, args[1])
	case "edit":
		if len(args) < 2 {
			return fmt.Errorf("theme name required: /theme:edit:<name>")
		}
		return c.editTheme(args[1])
	default:
		return fmt.Errorf("unknown subcommand: %s (try /theme:list, :set, :edit)", args[0])
	}
}

func (c *ThemeCommand) listThemes(ctx core.Context) error {
	names, err := c.Store.List()
	if err != nil {
		return fmt.Errorf("failed to list themes: %w", err)
	}
	if len(names) == 0 {
		ctx.Writef("No custom themes found.\n")
		return nil
	}
	active := c.Store.Active()
	activeName := ""
	if active != nil {
		activeName = active.Name
	}
	for _, n := range names {
		if n == activeName {
			ctx.Writef("* %s\n", n)
		} else {
			ctx.Writef("  %s\n", n)
		}
	}
	return nil
}

func (c *ThemeCommand) setTheme(ctx core.Context, name string) error {
	if err := c.Store.SetActive(name); err != nil {
		return fmt.Errorf("failed to set theme: %w", err)
	}
	ctx.Writef("Theme set to %s\n", name)
	return nil
}

func (c *ThemeCommand) editTheme(name string) error {
	editor := os.Getenv("EDITOR")
	if editor == "" {
		editor = "vi"
	}
	path := filepath.Join(c.Store.Dir(), name+".json")
	cmd := exec.Command(editor, path)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}
