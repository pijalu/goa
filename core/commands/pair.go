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

// PairCommand enables pair programming mode (planner + coder).
type PairCommand struct{}

func (c *PairCommand) Name() string      { return "pair" }
func (c *PairCommand) Aliases() []string { return []string{} }
func (c *PairCommand) ShortHelp() string { return "Enable pair programming mode" }
func (c *PairCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *PairCommand) Run(ctx core.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: /pair <task description>")
	}
	input := strings.Join(args, " ")

	orch := ctx.ForegroundOrchestrator
	if orch == nil {
		writeStr(ctx, "Orchestrator not available.\n")
		return nil
	}

	// Route orchestrator messages to TUI event bus
	// Orchestrator events are forwarded to the TUI by a single app-level
	// forwarder (App.runOrchestratorEventForwarder). Do not spawn a competing
	// consumer here — it would round-robin messages away from the real one.
	go func() {
		ctx.InterAgent("system", "user", "Pair session: planner is decomposing the task...")
		if err := orch.RunPair(orch.Context(), input); err != nil {
			ctx.InterAgent("system", "user", fmt.Sprintf("Pair error: %v", err))
		}
	}()

	writeFmt(ctx, "Pair session started for: %s\n", input)
	writeStr(ctx, "Planner and coder agents are running. Check chat for output.\n")
	return nil
}

// ReviewerCommand enables review mode (coder + reviewer).
type ReviewerCommand struct{}

func (c *ReviewerCommand) Name() string      { return "reviewer" }
func (c *ReviewerCommand) Aliases() []string { return []string{} }
func (c *ReviewerCommand) ShortHelp() string { return "Enable code review mode with reviewer agent" }
func (c *ReviewerCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *ReviewerCommand) Run(ctx core.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: /reviewer <task description>")
	}
	input := strings.Join(args, " ")

	orch := ctx.ForegroundOrchestrator
	if orch == nil {
		writeStr(ctx, "Orchestrator not available.\n")
		return nil
	}

	go func() {
		ctx.InterAgent("system", "user", "Review session: running review workflow...")
		if err := orch.RunWorkflow(orch.Context(), ctx.WorkflowRegistry, "review", input); err != nil {
			ctx.InterAgent("system", "user", fmt.Sprintf("Review error: %v", err))
		}
	}()

	writeFmt(ctx, "Review workflow started for: %s\n", input)
	writeStr(ctx, "Companion is analyzing the content. Check chat for output.\n")
	return nil
}

