// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
)

var _ = fmt.Errorf

// Deprecated: Mode handling has moved to core/commands/mode.go.
// The old /mode:command (execution.mode) is replaced by
// /mode <major> and /autonomy <yolo|confirm|review>.
// The

// StopCommand stops the currently running agent.
type StopCommand struct{}

func (c *StopCommand) Name() string      { return "stop" }
func (c *StopCommand) Aliases() []string { return []string{} }
func (c *StopCommand) ShortHelp() string { return "Stop the currently running agent" }
func (c *StopCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *StopCommand) Run(ctx core.Context, args []string) error {
	if ctx.AgentManager != nil {
		return ctx.AgentManager.Interrupt()
	}
	return nil
}

// RetryCommand retries the last agent turn.
type RetryCommand struct{}

func (c *RetryCommand) Name() string      { return "retry" }
func (c *RetryCommand) Aliases() []string { return []string{} }
func (c *RetryCommand) ShortHelp() string { return "Retry the last agent turn" }
func (c *RetryCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *RetryCommand) Run(ctx core.Context, args []string) error {
	if ctx.AgentManager == nil {
		writeStr(ctx, "No active session to retry.\n")
		return nil
	}
	input := ctx.AgentManager.LastUserInput()
	if input == "" {
		writeStr(ctx, "No previous user input to retry.\n")
		return nil
	}
	writeFmt(ctx, "Retrying: %s\n", input)
	return ctx.AgentManager.SendUserInput(input)
}

// UndoCommand reverts the last file change.
type UndoCommand struct{}

func (c *UndoCommand) Name() string      { return "undo" }
func (c *UndoCommand) Aliases() []string { return []string{} }
func (c *UndoCommand) ShortHelp() string { return "Revert the last file change" }
func (c *UndoCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *UndoCommand) Run(ctx core.Context, args []string) error {
	n := 1
	if len(args) > 0 {
		fmt.Sscanf(args[0], "%d", &n)
	}

	// Get list of changed (unstaged) files
	changes, err := listChangedFiles(ctx)
	if err != nil {
		writeFmt(ctx, "Error listing changes: %v\n", err)
		return nil
	}

	if len(changes) == 0 {
		// Check for staged changes
		staged, err := listStagedFiles(ctx)
		if err != nil {
			writeFmt(ctx, "Error listing staged changes: %v\n", err)
			return nil
		}
		if len(staged) == 0 {
			writeStr(ctx, "No changes to undo.\n")
			return nil
		}
		return undoStaged(ctx, staged, n)
	}

	return undoUnstaged(ctx, changes, n)
}

func listChangedFiles(ctx core.Context) ([]string, error) {
	cmd := exec.Command("git", "diff", "--name-only")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var result []string
	for _, l := range lines {
		if l != "" {
			result = append(result, l)
		}
	}
	return result, nil
}

func listStagedFiles(ctx core.Context) ([]string, error) {
	cmd := exec.Command("git", "diff", "--cached", "--name-only")
	output, err := cmd.Output()
	if err != nil {
		return nil, fmt.Errorf("git diff --cached: %w", err)
	}
	lines := strings.Split(strings.TrimSpace(string(output)), "\n")
	var result []string
	for _, l := range lines {
		if l != "" {
			result = append(result, l)
		}
	}
	return result, nil
}

func undoUnstaged(ctx core.Context, changes []string, n int) error {
	if n > len(changes) {
		n = len(changes)
	}
	targets := changes[:n]
	writeFmt(ctx, "Reverting %d file(s):\n", len(targets))
	for _, f := range targets {
		cmd := exec.Command("git", "checkout", "--", f)
		if err := cmd.Run(); err != nil {
			writeFmt(ctx, "  ✗ %s: %v\n", f, err)
		} else {
			writeFmt(ctx, "  ✓ %s\n", f)
		}
	}
	return nil
}

func undoStaged(ctx core.Context, staged []string, n int) error {
	if n > len(staged) {
		n = len(staged)
	}
	targets := staged[:n]
	writeFmt(ctx, "Unstaging %d file(s):\n", len(targets))
	for _, f := range targets {
		cmd := exec.Command("git", "restore", "--staged", f)
		if err := cmd.Run(); err != nil {
			writeFmt(ctx, "  ✗ %s: %v\n", f, err)
		} else {
			writeFmt(ctx, "  ✓ %s (unstaged)\n", f)
		}
	}
	return nil
}
