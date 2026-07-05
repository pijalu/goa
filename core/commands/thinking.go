// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal"
)

// ThinkingCommand sets or cycles the reasoning effort level.
type ThinkingCommand struct{}

func (c *ThinkingCommand) Name() string      { return "thinking" }
func (c *ThinkingCommand) Aliases() []string { return []string{} }
func (c *ThinkingCommand) ShortHelp() string { return "Set or show the thinking level" }

func (c *ThinkingCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *ThinkingCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	current := ""
	if ctx.AgentManager != nil {
		current = ctx.AgentManager.GetThinkingLevel()
	}
	var comps []core.ArgCompletion
	for _, level := range internal.AllThinkingLevels() {
		s := string(level)
		if s == current {
			continue
		}
		if strings.HasPrefix(s, prefix) {
			comps = append(comps, core.ArgCompletion{Value: s, Description: thinkingLevelDesc(s)})
		}
	}
	return comps
}

func thinkingLevelDesc(level string) string {
	switch internal.ThinkingLevel(level) {
	case internal.ThinkingLevelOff:
		return "no reasoning"
	case internal.ThinkingLevelMinimal:
		return "very brief reasoning (~1k tokens)"
	case internal.ThinkingLevelLow:
		return "light reasoning (~2k tokens)"
	case internal.ThinkingLevelMedium:
		return "moderate reasoning (~8k tokens)"
	case internal.ThinkingLevelHigh:
		return "deep reasoning (~16k tokens)"
	case internal.ThinkingLevelXHigh:
		return "maximum reasoning (~32k tokens)"
	default:
		return ""
	}
}

func (c *ThinkingCommand) Run(ctx core.Context, args []string) error {
	am := ctx.AgentManager
	if am == nil {
		writeStr(ctx, "Thinking level: unavailable (no active session)\n")
		return nil
	}

	if len(args) == 0 {
		current := am.GetThinkingLevel()
		if current == "" {
			current = "medium"
		}
		writeFmt(ctx, "Thinking level: %s (%s)\n", current, thinkingLevelDesc(current))
		return nil
	}

	level := args[0]
	if !internal.IsValidThinkingLevel(level) {
		writeFmt(ctx, "Unknown thinking level: %q. Valid: off, minimal, low, medium, high, xhigh\n", level)
		return nil
	}

	if err := am.SetThinkingLevel(level); err != nil {
		return fmt.Errorf("set thinking level: %w", err)
	}
	writeFmt(ctx, "Thinking level set to %s (%s).\n", level, thinkingLevelDesc(level))
	return nil
}
