// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
)

// HelpCommand shows command help and usage information.
type HelpCommand struct {
	Registry *core.CommandRegistry
}

func (c *HelpCommand) Name() string      { return "help" }
func (c *HelpCommand) Aliases() []string { return []string{} }
func (c *HelpCommand) ShortHelp() string { return "Show command help and usage information" }
func (c *HelpCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *HelpCommand) Run(ctx core.Context, args []string) error {
	if len(args) > 0 && args[0] != "" {
		return showHelpFor(ctx, c.Registry, ctx.ToolRegistry, ctx.DocsProvider, args[0])
	}
	return showFullHelp(ctx, c.Registry, ctx.ToolRegistry, ctx.DocsProvider)
}

func showHelpFor(out core.OutputWriter, cmdReg *core.CommandRegistry, reg core.ToolRegistry, dp core.DocsProvider, name string) error {
	if cmdReg != nil {
		cmd, found := cmdReg.Resolve(name)
		if found {
			writeFmt(out, "📋 /%s\n", cmd.Name())
			writeFmt(out, "   %s\n\n", cmd.ShortHelp())
			writeStr(out, cmd.LongHelp()+"\n")
			return nil
		}
	}

	if reg != nil {
		if _, ok := reg.Get(name); ok {
			writeFmt(out, "Tool '%s' — use /tools %s for details.\n", name, name)
			return nil
		}
	}

	if dp != nil {
		if info, err := dp.FindDocFile(name); err == nil {
			writeFmt(out, "Documentation '%s' — use /docs %s to view.\n", info.Name, info.Name)
			return nil
		}
	}

	writeFmt(out, "Unknown: %s. Use /help to list available commands.\n", name)
	return nil
}

func showFullHelp(out core.OutputWriter, cmdReg *core.CommandRegistry, reg core.ToolRegistry, dp core.DocsProvider) error {
	writeStr(out, "📋 Goa — terminal-native AI coding agent\n")
	writeStr(out, "======================================\n\n")

	writeStr(out, "Commands:\n")
	if cmdReg != nil {
		for _, cmd := range cmdReg.All() {
			name := cmd.Name()
			desc := cmd.ShortHelp()
			writeFmt(out, "  /%-25s %s\n", name, desc)
		}
	} else {
		writeStr(out, "  (command registry unavailable)\n")
	}

	printHelpTools(out, reg)
	printHelpDocs(out, dp)

	writeStr(out, "\nDocumentation suffixes: /cmd? = short help, /cmd?? = long help\n")
	writeStr(out, "Namespaces: cmd:name, tool:name, skill:name, docs:NAME\n")
	return nil
}

func printHelpTools(out core.OutputWriter, reg core.ToolRegistry) {
	writeStr(out, "\nTools:\n")
	if reg == nil {
		knownTools := []string{"read", "write", "edit", "search", "bash", "python", "ssh_bash", "bg_exec", "memento", "goa_command", "run_skill"}
		for _, name := range knownTools {
			writeFmt(out, "  %-25s (use /tools:%s or /docs:TOOLS for reference)\n", name, name)
		}
		return
	}
	for _, t := range reg.All() {
		schema := t.Schema()
		writeFmt(out, "  %-25s %s\n", schema.Name, schema.Description)
	}
}

func printHelpDocs(out core.OutputWriter, dp core.DocsProvider) {
	writeStr(out, "\nDocumentation:\n")
	if dp == nil {
		docs := []string{"ARCHITECTURE", "COMMANDS", "CONFIGURATION", "TOOLS", "SKILLS", "TUI", "PROFILES", "SETUP"}
		for _, name := range docs {
			writeFmt(out, "  %-25s (use /docs %s)\n", name, name)
		}
		return
	}
	docList, err := dp.List()
	if err != nil {
		return
	}
	for _, d := range docList {
		writeFmt(out, "  %-25s %s\n", d.Name, d.Description)
	}
}

// QuitCommand exits the application.
type QuitCommand struct{}

func (c *QuitCommand) Name() string      { return "quit" }
func (c *QuitCommand) Aliases() []string { return []string{} }
func (c *QuitCommand) ShortHelp() string { return "Exit Goa" }
func (c *QuitCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *QuitCommand) Run(ctx core.Context, args []string) error {
	ctx.StopRequest()
	return nil
}

// NewCommand clears the current session and starts a fresh one.
type NewCommand struct{}

func (c *NewCommand) Name() string      { return "new" }
func (c *NewCommand) Aliases() []string { return []string{} }
func (c *NewCommand) ShortHelp() string { return "Start a fresh session (clear chat and reset state)" }
func (c *NewCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *NewCommand) Run(ctx core.Context, args []string) error {
	return runNew(ctx)
}

func runNew(ctx core.Context) error {
	// Stop the current agent session cleanly so the LLM context is freed.
	if ctx.AgentManager != nil {
		if err := ctx.AgentManager.StopSession(); err != nil {
			writeFmt(ctx, "Warning: error stopping session: %v\n", err)
		}
	}

	// Signal the app to clear chat, reset stats, and start a fresh session.
	ctx.NewSession()
	return nil
}
