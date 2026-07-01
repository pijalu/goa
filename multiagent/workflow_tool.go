// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"fmt"

	"github.com/pijalu/goa/internal/agentic"
)

// WorkflowNextTool is an agent tool that advances the active workflow
// to the next stage. Each stage agent calls this when its work is complete.
type WorkflowNextTool struct {
	agentic.BaseTool
	Orchestrator *ForegroundOrchestrator
	Run          *PipelineRun
}

// Schema returns the tool schema for workflows:next.
func (t *WorkflowNextTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "workflows:next",
		Description: "Advance the current workflow stage to the next one.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"summary": map[string]any{
					"type":        "string",
					"description": "Summary of what was accomplished in this stage (files created/modified, decisions made). Only call this tool once all stage work is complete.",
				},
			},
			"required": []string{"summary"},
		},
	}
}

// Execute signals that the current stage is complete.
// It validates that the stage agent did real work, then cancels the current
// stage's agent context. The orchestrator (RunWorkflow) owns actual stage
// advancement by calling run.NextStage(); the tool must NOT advance the run
// itself, or Current and the stage status map become desynchronized.
func (t *WorkflowNextTool) Execute(input string) (string, error) {
	run := t.activeRun()
	if run == nil {
		return "", fmt.Errorf("no active workflow run; use /wf:run:<name> to start one")
	}

	if run.Status == PipelineCancelled {
		return "", fmt.Errorf("workflow has been cancelled")
	}

	// Require actual work before advancing. The orchestrator tracks how
	// many non-workflows:next tools were called during this stage.
	if t.Orchestrator != nil && t.Orchestrator.StageToolCount() == 0 {
		return "", fmt.Errorf("you must do the work before advancing; call write to write the files, then call workflows:next when done")
	}

	if t.Orchestrator != nil {
		t.Orchestrator.markStageAdvanced()
	}

	return "Stage complete. Advancing to next stage.", nil
}

// activeRun returns the current active pipeline run.
// Priority: 1) stored Run field (set at construction), 2) orchestrator's active run.
func (t *WorkflowNextTool) activeRun() *PipelineRun {
	if t.Run != nil {
		return t.Run
	}
	if t.Orchestrator != nil {
		return t.Orchestrator.ActiveRun()
	}
	return nil
}

// IsRetryable returns false — workflows:next should not be retried on failure.
func (t *WorkflowNextTool) IsRetryable(err error) bool { return false }
