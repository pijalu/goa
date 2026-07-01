// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal/logs/export"
)

// ExportCommand creates a self-contained diagnostic bundle for investigation.
type ExportCommand struct{}

func (c *ExportCommand) Name() string      { return "export" }
func (c *ExportCommand) Aliases() []string { return []string{} }
func (c *ExportCommand) IsInternal() bool  { return true }
func (c *ExportCommand) ShortHelp() string { return "Export a diagnostic bundle for debugging" }
func (c *ExportCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

// CompleteArgs returns no completions (path can be anything).
func (c *ExportCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	return nil
}

// Run executes the export command. When no issue description is supplied, it
// asks the host to capture the next main-input-line text as the description.
func (c *ExportCommand) Run(ctx core.Context, args []string) error {
	outputPath := ""
	if len(args) > 0 {
		outputPath = strings.TrimSpace(args[0])
	}

	issue := ""
	if len(args) > 1 {
		issue = strings.TrimSpace(strings.Join(args[1:], " "))
	}

	if issue != "" || ctx.RequestMainInput == nil {
		return runExport(ctx, outputPath, issue)
	}

	ctx.RequestMainInput("Describe the issue (optional), then press Enter:", func(value string) {
		if err := runExport(ctx, outputPath, value); err != nil {
			ctx.Flash(fmt.Sprintf("Export failed: %v", err))
		}
	})
	return nil
}

func runExport(ctx core.Context, outputPath, issue string) error {
	opts := export.BuildOptions{
		OutputPath:       outputPath,
		IssueDescription: issue,
		IncludeGlobalLog: true,
	}

	result, err := export.BuildBundle(ctx, opts)
	if err != nil {
		return err
	}

	ctx.Flash(fmt.Sprintf("Diagnostic bundle written to %s (%d entries, %d bytes)", result.Path, result.EntryCount, result.Size))
	return nil
}

// ExportSessionCommand is the headless-mode entry point for `goa export`.
type ExportSessionCommand struct {
	ProjectDir  string
	Issue       string
	OutputPath  string
	SessionID   string
	IncludeLogs bool
}

// Run executes the headless export.
func (c *ExportSessionCommand) Run(ctx core.Context) error {
	opts := export.BuildOptions{
		OutputPath:       c.OutputPath,
		IssueDescription: c.Issue,
		IncludeGlobalLog: c.IncludeLogs,
		ProjectDir:       c.ProjectDir,
		SessionID:        c.SessionID,
	}

	result, err := export.BuildBundle(ctx, opts)
	if err != nil {
		return err
	}

	ctx.Writef("%s\n", result.Path)
	return nil
}
