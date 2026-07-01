// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands/help"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/multiagent"
)

// WorkflowsCommand manages user-defined and built-in workflows.
type WorkflowsCommand struct{}

func (c *WorkflowsCommand) Name() string      { return "workflows" }
func (c *WorkflowsCommand) Aliases() []string { return []string{} }
func (c *WorkflowsCommand) ShortHelp() string { return "List, show, and run workflows" }
func (c *WorkflowsCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *WorkflowsCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	// Colon syntax: run:<name> or :<name>
	if colonIdx := strings.Index(prefix, ":"); colonIdx >= 0 {
		subCmd := prefix[:colonIdx]
		subArg := prefix[colonIdx+1:]
		if subCmd == "run" || subCmd == "" {
			return c.completeWorkflowNames(ctx, subArg)
		}
		return nil
	}

	// Standard subcommand completions
	var comps []core.ArgCompletion
	for _, v := range []struct{ val, desc string }{
		{"list", "list available workflows"},
		{"show", "show workflow details"},
		{"run", "run a workflow"},
	} {
		if prefix == "" || strings.HasPrefix(v.val, prefix) {
			comps = append(comps, core.ArgCompletion{Value: v.val, Description: v.desc})
		}
	}
	return comps
}

func (c *WorkflowsCommand) completeWorkflowNames(ctx core.Context, prefix string) []core.ArgCompletion {
	if ctx.WorkflowRegistry == nil {
		return nil
	}
	var comps []core.ArgCompletion
	for _, w := range ctx.WorkflowRegistry.All() {
		full := "run:" + w.ID
		if prefix == "" || strings.HasPrefix(full, "run:"+prefix) || strings.HasPrefix(w.ID, prefix) {
			comps = append(comps, core.ArgCompletion{
				Value:       full,
				Description: w.Description,
			})
		}
	}
	return comps
}

func (c *WorkflowsCommand) Run(ctx core.Context, args []string) error {
	if len(args) == 0 {
		return listWorkflows(ctx)
	}

	// Parse colon syntax: run:name, :name (shorthand for run)
	cmd := args[0]
	remaining := args[1:]
	if colonIdx := strings.Index(cmd, ":"); colonIdx >= 0 {
		prefix := cmd[:colonIdx]
		suffix := cmd[colonIdx+1:]
		if prefix == "run" || prefix == "" {
			// run:name or :name → treat as run
			return runWorkflow(ctx, append([]string{suffix}, remaining...))
		}
		return fmt.Errorf("unknown workflows command: %s", cmd)
	}

	switch cmd {
	case "list":
		return listWorkflows(ctx)
	case "show":
		return showWorkflow(ctx, remaining)
	case "run":
		return runWorkflow(ctx, remaining)
	case "cancel":
		return cancelWorkflow(ctx)
	default:
		// Bare workflow name → treat as run shorthand
		return runWorkflow(ctx, append([]string{cmd}, remaining...))
	}
}

func listWorkflows(ctx core.Context) error {
	reg := ctx.WorkflowRegistry
	if reg == nil {
		writeStr(ctx, "Workflow registry not available.\n")
		return nil
	}

	workflows := reg.All()
	if len(workflows) == 0 {
		writeStr(ctx, "No workflows available.\n")
		return nil
	}

	writeStr(ctx, "Available workflows:\n\n")
	for _, w := range workflows {
		boxWidth := 66
		title := fmt.Sprintf(" %s ", w.Name)
		titleWidth := ansi.Width(title)
		dashCount := boxWidth - titleWidth - 2 // 2 for ┌ ┐
		if dashCount < 1 {
			dashCount = 1
		}

		// Top border: ┌─ Title ──────────────────────┐
		writeFmt(ctx, "┌%s%s┐\n", title, strings.Repeat("─", dashCount))

		// Description
		desc := truncateTo(w.Description, boxWidth-4)
		descWidth := ansi.Width(desc)
		pad := boxWidth - 4 - descWidth
		if pad < 0 {
			pad = 0
		}
		writeFmt(ctx, "│ %s%s │\n", desc, strings.Repeat(" ", pad))

		// Stages arrow format
		stageNames := make([]string, len(w.Stages))
		for i, s := range w.Stages {
			stageNames[i] = s.Name
		}
		stageStr := fmt.Sprintf("%d stage(s): %s", len(w.Stages), strings.Join(stageNames, " → "))
		stageWidth := ansi.Width(stageStr)
		pad = boxWidth - 4 - stageWidth
		if pad < 0 {
			pad = 0
		}
		writeFmt(ctx, "│ %s%s │\n", stageStr, strings.Repeat(" ", pad))

		// Runnable ID
		runStr := fmt.Sprintf("Run: /workflows:run:%s", w.ID)
		runWidth := ansi.Width(runStr)
		pad = boxWidth - 4 - runWidth
		if pad < 0 {
			pad = 0
		}
		writeFmt(ctx, "│ %s%s │\n", runStr, strings.Repeat(" ", pad))

		// Bottom border
		writeFmt(ctx, "└%s┘\n\n", strings.Repeat("─", boxWidth-2))
	}
	return nil
}

func truncateTo(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen-3]) + "..."
}

func showWorkflow(ctx core.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: /workflows:show <name>")
	}

	reg := ctx.WorkflowRegistry
	if reg == nil {
		writeStr(ctx, "Workflow registry not available.\n")
		return nil
	}

	w, ok := reg.Get(args[0])
	if !ok {
		writeFmt(ctx, "Workflow not found: %s\n", args[0])
		return nil
	}

	writeFmt(ctx, "Workflow: %s\n", w.Name)
	writeFmt(ctx, "ID: %s\n", w.ID)
	writeFmt(ctx, "Description: %s\n", w.Description)
	writeFmt(ctx, "Stages: %d\n\n", len(w.Stages))

	for i, s := range w.Stages {
		writeFmt(ctx, "  %d. %s\n", i+1, s.Name)
		writeFmt(ctx, "     Agent: %s\n", s.Agent)
		if s.Gate.RequireApproval {
			writeFmt(ctx, "     Gate: requires approval\n")
		}
		promptPreview := s.Prompt
		if len(promptPreview) > 80 {
			promptPreview = promptPreview[:77] + "..."
		}
		writeFmt(ctx, "     Prompt: %s\n", promptPreview)
	}
	return nil
}

func runWorkflow(ctx core.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: /workflows:run <name> [input]")
	}

	reg := ctx.WorkflowRegistry
	if reg == nil {
		writeStr(ctx, "Workflow registry not available.\n")
		return nil
	}

	// The router may have merged workflow name + description via colon splitting.
	// e.g., ":run:implement-feature Create an HTML page" → args[0] = "implement-feature Create an HTML page"
	// Split by first space to separate them.
	fullArg := args[0]
	name := fullArg
	input := strings.Join(args[1:], " ")
	if spaceIdx := strings.Index(fullArg, " "); spaceIdx >= 0 {
		name = fullArg[:spaceIdx]
		rest := strings.TrimSpace(fullArg[spaceIdx+1:])
		if rest != "" {
			if input != "" {
				input = rest + " " + input
			} else {
				input = rest
			}
		}
	}

	w, ok := reg.Get(name)
	if !ok {
		writeFmt(ctx, "Workflow not found: %s. Use /workflows:list to see available workflows.\n", name)
		return nil
	}

	orch := ctx.ForegroundOrchestrator
	if orch == nil {
		writeStr(ctx, "Orchestrator not available. Cannot run workflow.\n")
		return nil
	}

	if input == "" {
		promptWorkflowInput(ctx, reg, orch, w)
		return nil
	}

	startWorkflowRun(ctx, reg, orch, w, input)
	return nil
}

func promptWorkflowInput(ctx core.Context, reg *multiagent.WorkflowRegistry, orch *multiagent.ForegroundOrchestrator, w multiagent.Pipeline) {
	writeWorkflowHeader(ctx, w)
	ctx.ShowInput("Describe feature to implement:", "", func(value string, ok bool) {
		if !ok || value == "" {
			ctx.InterAgent("workflows", "user", "Workflow cancelled.")
			return
		}
		startWorkflowRun(ctx, reg, orch, w, value)
	})
}

func writeWorkflowHeader(ctx core.Context, w multiagent.Pipeline) {
	writeFmt(ctx, "Starting workflow: %s\n", w.Name)
	writeFmt(ctx, "Description: %s\n", w.Description)
	writeFmt(ctx, "Stages: %d\n\n", len(w.Stages))
	for i, stage := range w.Stages {
		writeFmt(ctx, "  %d. [%s] %s\n", i+1, stage.Agent, stage.Name)
	}
}

func cancelWorkflow(ctx core.Context) error {
	orch := ctx.ForegroundOrchestrator
	if orch == nil {
		writeStr(ctx, "Orchestrator not available.\n")
		return nil
	}
	orch.Cancel()
	writeStr(ctx, "Workflow cancellation requested.\n")
	return nil
}

func startWorkflowRun(ctx core.Context, reg *multiagent.WorkflowRegistry, orch *multiagent.ForegroundOrchestrator, w multiagent.Pipeline, input string) {
	writeFmt(ctx, "Starting workflow: %s\n", w.Name)
	writeFmt(ctx, "Description: %s\n", w.Description)
	writeFmt(ctx, "Stages: %d\n\n", len(w.Stages))
	for i, stage := range w.Stages {
		writeFmt(ctx, "  %d. [%s] %s\n", i+1, stage.Agent, stage.Name)
	}

	// Suspend companion mode during workflow execution — the workflow
	// defines its own agent orchestration. Save and restore the mode.
	orch.SuspendCompanion()

	go func() {
		if err := orch.RunWorkflow(orch.Context(), reg, w.ID, input); err != nil {
			ctx.InterAgent("workflows", "user", fmt.Sprintf("Workflow error: %v", err))
		}
		// Restore companion mode after workflow completes
		orch.ResumeCompanion()
	}()

	inputMsg := ""
	if input != "" {
		inputMsg = fmt.Sprintf(" with input: %s", input)
	}
	writeFmt(ctx, "\nWorkflow '%s' started%s. Type /workflows:cancel to abort.\n", w.ID, inputMsg)
}
