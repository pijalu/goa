// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plan

import (
	"embed"
	"encoding/json"
	"fmt"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/tools/common"
)

// TaskOutcomeTool is a worker-only tool that reports the outcome of a plan item
// execution. It implements ResultTool with StopTurn: true always.
type TaskOutcomeTool struct {
	agentic.BaseTool
}

//go:embed task_outcome.short.md task_outcome.long.md
var taskOutcomeDocs embed.FS

// ShortDoc returns a short doc string.
func (t *TaskOutcomeTool) ShortDoc() string { return common.ReadDoc(taskOutcomeDocs, "task_outcome.short.md") }

// LongDoc returns a long doc string.
func (t *TaskOutcomeTool) LongDoc() string { return common.ReadDoc(taskOutcomeDocs, "task_outcome.long.md") }

// Examples returns usage examples.
func (t *TaskOutcomeTool) Examples() []string {
	return []string{
		`{"status": "done", "summary": "Implemented the API endpoint"}`,
		`{"status": "needs_clarification", "summary": "Unclear about the port", "question": "Which port should the service listen on?"}`,
		`{"status": "blocked", "summary": "Missing database credentials"}`,
	}
}

// taskOutcomeInput is the expected input shape.
type taskOutcomeInput struct {
	Status   string `json:"status"`
	Summary  string `json:"summary"`
	Question string `json:"question,omitempty"`
}

// Schema returns the tool's JSON schema.
func (t *TaskOutcomeTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "task_outcome",
		Description: "Report the outcome of a plan item execution. Always stops the turn.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"status": map[string]any{
					"type":        "string",
					"description": "Outcome: done, needs_clarification, or blocked",
					"enum":        []string{"done", "needs_clarification", "blocked"},
				},
				"summary": map[string]any{
					"type":        "string",
					"description": "Result summary or reason. Max 4000 characters.",
				},
				"question": map[string]any{
					"type":        "string",
					"description": "Required when status is needs_clarification. The question for the orchestrator.",
				},
			},
			"required": []string{"status", "summary"},
		},
	}
}

// Execute implements agentic.Tool.
func (t *TaskOutcomeTool) Execute(input string) (string, error) {
	result, err := t.ExecuteWithResult(input)
	if err != nil {
		return "", err
	}
	return result.Output, nil
}

// ExecuteWithResult implements agentic.ResultTool.
func (t *TaskOutcomeTool) ExecuteWithResult(input string) (agentic.ToolResult, error) {
	var in taskOutcomeInput
	if err := json.Unmarshal([]byte(input), &in); err != nil {
		return agentic.ToolResult{}, &internal.ToolError{
			Tool:     "task_outcome",
			Type:     "invalid_input",
			Detail:   fmt.Sprintf("invalid JSON: %v", err),
			HintText: "Provide a valid JSON object with status and summary fields.",
		}
	}

	switch in.Status {
	case "done":
		if in.Summary == "" {
			return agentic.ToolResult{}, &internal.ToolError{
				Tool:     "task_outcome",
				Type:     "invalid_input",
				Detail:   "summary is required for status 'done'",
				HintText: "Provide a summary of what was accomplished.",
			}
		}
	case "needs_clarification":
		if in.Question == "" {
			return agentic.ToolResult{}, &internal.ToolError{
				Tool:     "task_outcome",
				Type:     "invalid_input",
				Detail:   "question is required for status 'needs_clarification'",
				HintText: "Provide the question that needs clarification.",
			}
		}
		if in.Summary == "" {
			in.Summary = "Clarification needed"
		}
	case "blocked":
		if in.Summary == "" {
			return agentic.ToolResult{}, &internal.ToolError{
				Tool:     "task_outcome",
				Type:     "invalid_input",
				Detail:   "summary is required for status 'blocked'",
				HintText: "Provide the reason the item is blocked.",
			}
		}
	default:
		return agentic.ToolResult{}, &internal.ToolError{
			Tool:     "task_outcome",
			Type:     "invalid_input",
			Detail:   fmt.Sprintf("unknown status %q", in.Status),
			HintText: "Status must be one of: done, needs_clarification, blocked.",
		}
	}

	// Truncate summary to 4000 characters.
	if len(in.Summary) > 4000 {
		in.Summary = in.Summary[:4000] + " [truncated]"
	}

	// Return the typed outcome as canonical JSON.
	out := map[string]string{
		"status":  in.Status,
		"summary": in.Summary,
	}
	if in.Question != "" {
		out["question"] = in.Question
	}

	resultJSON, _ := json.Marshal(out)
	return agentic.ToolResult{
		Output:   string(resultJSON),
		StopTurn: true,
	}, nil
}
