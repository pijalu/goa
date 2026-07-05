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

// ThinkingBlocksCommand toggles whether main-agent thinking blocks are
// expanded or collapsed in the TUI.
type ThinkingBlocksCommand struct{}

func (c *ThinkingBlocksCommand) Name() string      { return "thinking-blocks" }
func (c *ThinkingBlocksCommand) Aliases() []string { return []string{} }
func (c *ThinkingBlocksCommand) ShortHelp() string {
	return "Toggle main-agent thinking block expansion"
}

func (c *ThinkingBlocksCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *ThinkingBlocksCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	cfg := ctx.Config
	collapsed := cfg != nil && cfg.TUI.Transparency.ThinkingCollapsed
	next := "off"
	desc := "collapse thinking blocks"
	if collapsed {
		next = "on"
		desc = "expand thinking blocks"
	}
	if prefix != "" && !strings.HasPrefix(next, prefix) {
		return nil
	}
	return []core.ArgCompletion{{Value: next, Description: desc}}
}

func (c *ThinkingBlocksCommand) Run(ctx core.Context, args []string) error {
	cfg := ctx.Config
	if cfg == nil {
		writeStr(ctx, "Thinking blocks toggle: unavailable (no config)\n")
		return nil
	}

	if len(args) == 0 {
		if cfg.TUI.Transparency.ThinkingCollapsed {
			writeStr(ctx, "Thinking blocks: collapsed (use /thinking-blocks:on to expand)\n")
		} else {
			writeStr(ctx, "Thinking blocks: expanded (use /thinking-blocks:off to collapse)\n")
		}
		return nil
	}

	switch args[0] {
	case "on":
		cfg.TUI.Transparency.ThinkingCollapsed = false
		if err := saveThinkingBlocksPreference(ctx, false); err != nil {
			return err
		}
		writeStr(ctx, "Thinking blocks expanded.\n")
	case "off":
		cfg.TUI.Transparency.ThinkingCollapsed = true
		if err := saveThinkingBlocksPreference(ctx, true); err != nil {
			return err
		}
		writeStr(ctx, "Thinking blocks collapsed.\n")
	default:
		writeFmt(ctx, "Unknown option: %q. Use /thinking-blocks:on or /thinking-blocks:off\n", args[0])
	}
	return nil
}

// saveThinkingBlocksPreference persists only the thinking-collapsed preference
// to ~/.goa/config.yaml without overwriting other user settings.
func saveThinkingBlocksPreference(ctx core.Context, collapsed bool) error {
	if ctx.ConfigSaver == nil {
		return nil
	}
	if err := ctx.ConfigSaver.SaveHomeField([]string{"tui", "transparency", "thinking_collapsed"}, collapsed); err != nil {
		return fmt.Errorf("save thinking-blocks preference: %w", err)
	}
	return nil
}
