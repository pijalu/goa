// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/multiagent"
)

// OrchestrateCommand runs a sequence of tasks across agent roles.
type OrchestrateCommand struct{}

func (c *OrchestrateCommand) Name() string      { return "orchestrate" }
func (c *OrchestrateCommand) Aliases() []string { return []string{} }
func (c *OrchestrateCommand) ShortHelp() string { return "Run task orchestration across agent roles" }
func (c *OrchestrateCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *OrchestrateCommand) Run(ctx core.Context, args []string) error {
	if len(args) == 0 {
		return fmt.Errorf("usage: /orchestrate followed by comma-separated tasks")
	}
	input := strings.Join(args, " ")
	tasks := multiagent.ParseTasks(input)

	if len(tasks) == 0 {
		writeStr(ctx, "No tasks specified. Use: /orchestrate <task1>, <task2>, ...\n")
		return nil
	}

	orch := ctx.ForegroundOrchestrator
	if orch == nil {
		writeStr(ctx, "Orchestrator not available.\n")
		return nil
	}

	// Orchestrator events are forwarded to the TUI by a single app-level
	// forwarder (App.runOrchestratorEventForwarder). Do not spawn a competing
	// consumer here — it would round-robin messages away from the real one.

	taskOrch := multiagent.NewTaskOrchestrator(orch)

	writeFmt(ctx, "Orchestration started for %d tasks.\n", len(tasks))
	for i, t := range tasks {
		writeFmt(ctx, "  %d. %s\n", i+1, t.Description)
	}

	go func() {
		for _, t := range tasks {
			ctx.Flash(fmt.Sprintf("Task: %s", t.Description))
		}
		if err := taskOrch.RunSequential(orch.Context(), tasks); err != nil {
			ctx.InterAgent("orchestrator", "user", fmt.Sprintf("Orchestration error: %v", err))
		}
	}()

	return nil
}
