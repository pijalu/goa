// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
)

// ReloadCommand reloads skills, context files, and plugins at runtime.
// It re-scans all skill directories, re-loads AGENTS.md context files,
// and re-registers skill shortcut commands.
type ReloadCommand struct{}

func (c *ReloadCommand) Name() string      { return "reload" }
func (c *ReloadCommand) Aliases() []string { return []string{} }
func (c *ReloadCommand) ShortHelp() string { return "Reload skills, context, and plugins" }
func (c *ReloadCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *ReloadCommand) Run(ctx core.Context, args []string) error {
	if ctx.ReloadHandler == nil {
		return fmt.Errorf("reload handler not available")
	}

	var summary []string

	// Reload skills
	count, err := ctx.ReloadHandler.ReloadSkills()
	if err != nil {
		summary = append(summary, fmt.Sprintf("Skills: error — %v", err))
	} else {
		summary = append(summary, fmt.Sprintf("Skills: %d loaded", count))
	}

	// Reload context files
	ctxCount, err := ctx.ReloadHandler.ReloadContext()
	if err != nil {
		summary = append(summary, fmt.Sprintf("Context: error — %v", err))
	} else if ctxCount > 0 {
		summary = append(summary, fmt.Sprintf("Context: %d file(s) loaded", ctxCount))
	} else {
		summary = append(summary, "Context: none found")
	}

	// Reload plugins
	if err := ctx.ReloadHandler.ReloadPlugins(); err != nil {
		summary = append(summary, fmt.Sprintf("Plugins: error — %v", err))
	} else {
		summary = append(summary, "Plugins: reloaded")
	}

	ctx.Writef("Reload complete:\n")
	for _, s := range summary {
		ctx.Writef("  • %s\n", s)
	}
	return nil
}
