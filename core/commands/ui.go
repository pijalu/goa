// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
)

// UICommand provides LLM-controlled UI changes.
type UICommand struct{}

func (c *UICommand) Name() string      { return "ui" }
func (c *UICommand) Aliases() []string { return []string{} }
func (c *UICommand) ShortHelp() string { return "Control UI elements (theme, panes, flash)" }
func (c *UICommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *UICommand) Run(ctx core.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: /ui:<action>[:args]")
	}

	switch args[0] {
	case "theme":
		return handleUITheme(ctx, args[1:])
	case "pane":
		return handleUIPane(ctx, args[1:])
	case "flash":
		return handleUIFlash(ctx, args[1:])
	default:
		return fmt.Errorf("unknown ui action: %s (use theme, pane, or flash)", args[0])
	}
}

// CompleteArgs offers colon-aware completions for /ui:
//
// 	/ui:<empty>      → theme, pane, flash
// 	/ui:theme:       → set
// 	/ui:pane:        → show, hide
func (c *UICommand) CompleteArgs(_ core.Context, prefix string) []core.ArgCompletion {
	parts := strings.Split(prefix, ":")
	switch len(parts) {
	case 1:
		var comps []core.ArgCompletion
		for _, v := range []struct{ val, desc string }{
			{"theme", "change theme tokens"},
			{"pane", "show or hide a pane"},
			{"flash", "show a flash message"},
		} {
			if strings.HasPrefix(v.val, parts[0]) {
				comps = append(comps, core.ArgCompletion{Value: v.val, Description: v.desc})
			}
		}
		return comps
	case 2:
		sub := parts[0]
		tail := parts[1]
		var opts []string
		switch sub {
		case "theme":
			opts = []string{"set"}
		case "pane":
			opts = []string{"show", "hide"}
		}
		var comps []core.ArgCompletion
		for _, o := range opts {
			if strings.HasPrefix(o, tail) {
				comps = append(comps, core.ArgCompletion{Value: o})
			}
		}
		return comps
	default:
		return nil
	}
}

func handleUITheme(ctx core.Context, args []string) error {
	if len(args) < 2 || args[0] != "set" {
		return fmt.Errorf("usage: /ui theme set <token> <color>")
	}
	writeFmt(ctx, "Theme token %s set to %s\n", args[1], args[2])
	return nil
}

func handleUIPane(ctx core.Context, args []string) error {
	if len(args) < 2 {
		return fmt.Errorf("usage: /ui pane <show|hide> <id>")
	}
	action := args[0]
	id := strings.Join(args[1:], " ")
	if action == "show" {
		writeFmt(ctx, "Showing pane: %s\n", id)
		// ShowOutputModal event removed with typed event bus; use flash for now.
		ctx.Flash("Pane: " + id)
	} else if action == "hide" {
		writeFmt(ctx, "Hiding pane: %s\n", id)
		ctx.Flash("Hide pane")
	} else {
		return fmt.Errorf("usage: /ui pane <show|hide> <id>")
	}
	return nil
}

func handleUIFlash(ctx core.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: /ui flash <message>")
	}
	msg := strings.Join(args, " ")
	writeFmt(ctx, "⚡ %s\n", msg)
	ctx.Flash(msg)
	return nil
}
