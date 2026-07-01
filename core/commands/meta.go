// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"runtime"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal"
)

// GoaCommand provides information about Goa itself.
type GoaCommand struct{}

func (c *GoaCommand) Name() string      { return "goa" }
func (c *GoaCommand) Aliases() []string { return []string{} }
func (c *GoaCommand) ShortHelp() string { return "Show information about Goa" }
func (c *GoaCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *GoaCommand) Run(ctx core.Context, args []string) error {
	writeStr(ctx, "Goa — terminal-native AI coding agent\n")
	writeFmt(ctx, "Version: %s\n", internal.Version)
	writeFmt(ctx, "Go: %s %s/%s\n", runtime.Version(), runtime.GOOS, runtime.GOARCH)
	writeFmt(ctx, "Config: %s\n", ctx.Config.ConfigDir)
	writeFmt(ctx, "Profile: %s\n", ctx.Config.ActiveMajor())
	writeFmt(ctx, "Mode: %s\n", ctx.Config.Execution.Mode)
	return nil
}

// VersionCommand shows the version.
type VersionCommand struct{}

func (c *VersionCommand) Name() string      { return "version" }
func (c *VersionCommand) Aliases() []string { return []string{} }
func (c *VersionCommand) ShortHelp() string { return "Show Goa version" }
func (c *VersionCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *VersionCommand) Run(ctx core.Context, args []string) error {
	writeFmt(ctx, "Goa v%s\n", internal.Version)
	return nil
}

// DebugCommand enables or shows debug information.
type DebugCommand struct{}

func (c *DebugCommand) Name() string      { return "debug" }
func (c *DebugCommand) Aliases() []string { return []string{} }
func (c *DebugCommand) ShortHelp() string { return "Enable or show debug information" }
func (c *DebugCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *DebugCommand) Run(ctx core.Context, args []string) error {
	writeStr(ctx, "Goa Debug Information\n")
	writeStr(ctx, "=====================\n\n")
	writeFmt(ctx, "Version: %s\n", internal.Version)
	writeFmt(ctx, "Config Dir: %s\n", ctx.Config.ConfigDir)
	writeFmt(ctx, "Active Profile: %s\n", ctx.Config.ActiveMajor())
	writeFmt(ctx, "Active Provider: %s\n", ctx.Config.ActiveProvider)
	writeFmt(ctx, "Active Model: %s\n", ctx.Config.ActiveModel)
	writeFmt(ctx, "Execution Mode: %s\n", ctx.Config.Execution.Mode)

	if ctx.AgentManager != nil {
		writeFmt(ctx, "Session Mode: %s\n", ctx.AgentManager.CurrentMode().String())
		if agent := ctx.AgentManager.CurrentAgent(); agent != nil {
			writeStr(ctx, "Agent: active\n")
		} else {
			writeStr(ctx, "Agent: idle\n")
		}
	} else {
		writeStr(ctx, "Session: inactive\n")
	}

	if ctx.ToolRegistry != nil {
		writeFmt(ctx, "Tools: %d registered\n", len(ctx.ToolRegistry.All()))
	}
	if ctx.SkillRegistry != nil {
		writeFmt(ctx, "Skills: %d available\n", len(ctx.SkillRegistry.List()))
	}
	if ctx.MemoryStore != nil {
		if files, err := ctx.MemoryStore.List(); err == nil {
			writeFmt(ctx, "Memory files: %d\n", len(files))
		}
	}
	if ctx.SessionStore != nil {
		if sessions, err := ctx.SessionStore.ListSessions(); err == nil {
			writeFmt(ctx, "Saved sessions: %d\n", len(sessions))
		}
	}
	return nil
}
