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

// PipelineCommand manages multi-agent pipelines.
type PipelineCommand struct{}

func (c *PipelineCommand) Name() string      { return "pipeline" }
func (c *PipelineCommand) Aliases() []string { return []string{} }
func (c *PipelineCommand) ShortHelp() string { return "Manage multi-agent pipelines" }
func (c *PipelineCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *PipelineCommand) CompleteArgs(ctx core.Context, prefix string) []core.ArgCompletion {
	var comps []core.ArgCompletion
	for _, v := range []struct{ val, desc string }{{"list", "list available pipelines"}, {"run", "run a pipeline"}, {"status", "show pipeline status"}} {
		if prefix == "" || strings.HasPrefix(v.val, prefix) {
			comps = append(comps, core.ArgCompletion{Value: v.val, Description: v.desc})
		}
	}
	return comps
}

func (c *PipelineCommand) Run(ctx core.Context, args []string) error {
	if len(args) == 0 {
		// Default action: list available pipelines
		return listPipelines(ctx)
	}

	switch args[0] {
	case "list":
		return listPipelines(ctx)
	case "run":
		return runPipeline(ctx, args[1:])
	case "status":
		return pipelineStatus(ctx)
	default:
		return fmt.Errorf("unknown pipeline command: %s (use list, run, or status)", args[0])
	}
}

func listPipelines(ctx core.Context) error {
	pipelines := multiagent.BuiltinPipelines()
	if len(pipelines) == 0 {
		writeStr(ctx, "No pipelines available.\n")
		return nil
	}
	writeStr(ctx, "Available pipelines:\n\n")
	for _, p := range pipelines {
		writeFmt(ctx, "  %-20s %s\n", p.ID, p.Name)
		writeFmt(ctx, "  %-20s %s\n", "", p.Description)
		writeFmt(ctx, "  %-20s %d stages:", "", len(p.Stages))
		for i, s := range p.Stages {
			gate := ""
			if s.Gate.RequireApproval {
				gate = " [gate: requires approval]"
			}
			writeFmt(ctx, " %d. %s (%s)%s", i+1, s.Name, s.Agent, gate)
		}
		writeStr(ctx, "\n\n")
	}
	return nil
}

func runPipeline(ctx core.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: /pipeline:run <name> [input]")
	}

	name := args[0]
	input := strings.Join(args[1:], " ")

	selected := resolvePipeline(name)
	if selected == nil {
		writeFmt(ctx, "Pipeline not found: %s. Use /pipeline:list to see available pipelines.\n", name)
		return nil
	}

	if ctx.PipelineRunner == nil {
		writeStr(ctx, "Pipeline runner not available. Cannot execute pipeline.\n")
		return nil
	}

	printPipelineHeader(ctx, selected)
	startPipelineRun(ctx, selected, input)
	return nil
}

func resolvePipeline(name string) *multiagent.Pipeline {
	for _, p := range multiagent.BuiltinPipelines() {
		if p.ID == name {
			return &p
		}
	}
	return nil
}

func printPipelineHeader(ctx core.Context, p *multiagent.Pipeline) {
	writeFmt(ctx, "Starting pipeline: %s\n", p.Name)
	writeFmt(ctx, "Description: %s\n", p.Description)
	writeFmt(ctx, "Stages: %d\n\n", len(p.Stages))
	for i, stage := range p.Stages {
		gate := ""
		if stage.Gate.RequireApproval {
			gate = " (requires approval)"
		}
		writeFmt(ctx, "  %d. [%s] %s%s\n", i+1, stage.Agent, stage.Name, gate)
	}
}

func startPipelineRun(ctx core.Context, selected *multiagent.Pipeline, input string) {
	run := multiagent.NewPipelineRun(selected)
	ctx.ActivePipelineRun = run

	// Pipeline events are forwarded to the TUI by a single app-level
	// forwarder (App.runPipelineEventForwarder). Do not spawn a competing
	// consumer here — repeated /pipeline:run calls would round-robin events
	// away from the real forwarder.
	backgroundRunPipeline(ctx, run)

	inputMsg := ""
	if input != "" {
		inputMsg = fmt.Sprintf(" with input: %s", input)
	}
	writeFmt(ctx, "Pipeline '%s' started%s. Use /pipeline:status to check progress.\n", selected.ID, inputMsg)
}


func backgroundRunPipeline(ctx core.Context, run *multiagent.PipelineRun) {
	go func() {
		if err := ctx.PipelineRunner.Run(run); err != nil {
			ctx.InterAgent("pipeline", "user", fmt.Sprintf("pipeline %s failed: %v", run.Pipeline.ID, err))
		}
	}()
}

func pipelineStatus(ctx core.Context) error {
	run := ctx.ActivePipelineRun
	if run == nil {
		writeStr(ctx, "No active pipeline. Use /pipeline:run <name> to start one.\n")
		return nil
	}

	status, current, stages := run.StatusSnapshot()
	switch status {
	case multiagent.PipelineCompleted:
		writeFmt(ctx, "Pipeline '%s' completed successfully.\n", run.Pipeline.ID)
		renderPipelineStages(ctx, run, stages)
		return nil
	case multiagent.PipelineFailed:
		writeFmt(ctx, "Pipeline '%s' failed.\n", run.Pipeline.ID)
		renderPipelineStages(ctx, run, stages)
		return nil
	case multiagent.PipelineCancelled:
		writeFmt(ctx, "Pipeline '%s' cancelled.\n", run.Pipeline.ID)
		renderPipelineStages(ctx, run, stages)
		return nil
	case "":
		writeStr(ctx, "No active pipeline. Use /pipeline:run <name> to start one.\n")
		return nil
	}

	writeFmt(ctx, "Pipeline status: %s\n", status)
	if status == multiagent.PipelinePending {
		writeStr(ctx, "Pipeline is paused at an approval gate. Use /go to continue.\n")
	}
	writeFmt(ctx, "Current stage: %d/%d\n", current+1, len(run.Pipeline.Stages))
	renderPipelineStages(ctx, run, stages)
	return nil
}

func renderPipelineStages(ctx core.Context, run *multiagent.PipelineRun, stages map[string]multiagent.StageStatus) {
	writeStr(ctx, "Stages:\n")
	for _, stage := range run.Pipeline.Stages {
		stageStatus := stages[stage.ID]
		marker := "\u25CB"
		switch stageStatus {
		case multiagent.StageCompleted:
			marker = "\u2713"
		case multiagent.StageRunning:
			marker = "\u25B6"
		case multiagent.StageFailed:
			marker = "\u2717"
		case multiagent.StagePaused:
			marker = "\u23F8"
		}
		writeFmt(ctx, "  %s %s\n", marker, stage.Name)
	}
}

// GoCommand continues a paused pipeline through a gate.
type GoCommand struct{}

func (c *GoCommand) Name() string      { return "go" }
func (c *GoCommand) Aliases() []string { return []string{} }
func (c *GoCommand) ShortHelp() string { return "Continue a paused pipeline through a gate" }
func (c *GoCommand) LongHelp() string {
	return help.LongHelp(c.Name())
}

func (c *GoCommand) Run(ctx core.Context, args []string) error {
	run := ctx.ActivePipelineRun
	if run == nil {
		writeStr(ctx, "No pipeline is currently paused at a gate. Use /pipeline:run <name> to start one.\n")
		return nil
	}

	status, current, _ := run.StatusSnapshot()
	if status != multiagent.PipelinePending {
		writeStr(ctx, "No pipeline is currently paused at a gate. Use /pipeline:run <name> to start one.\n")
		return nil
	}

	// Resume(true) submits the approval decision on the run's gate channel.
	// RunWithContext blocks at waitGateApproval() until this fires (or ctx is
	// cancelled), so /go is what actually unblocks the paused pipeline.
	if !run.Resume(true) {
		writeStr(ctx, "Could not continue: the pipeline is no longer waiting at a gate.\n")
		return nil
	}
	writeFmt(ctx, "Continuing pipeline '%s' from stage %d...\n", run.Pipeline.ID, current+1)
	writeStr(ctx, "Gate approved. Pipeline continuing.\n")
	return nil
}
