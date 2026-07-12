// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/core/tasks"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

// AgentTool spawns isolated sub-agents to execute focused tasks.
type AgentTool struct {
	agentic.BaseTool
	Pool         *AgentPool
	ModeResolver ModeResolver
	// Orchestrator receives lifecycle messages when a foreground sub-agent
	// starts and finishes, so the TUI can show it in the orchestration view.
	Orchestrator interface {
		Emit(from, to, content string)
	}
	// TaskBus tracks background agents and delivers lifecycle events.
	TaskBus *tasks.Bus
	// OnBackgroundResult is called when a background agent completes.
	OnBackgroundResult func(taskID, result string, err error)
	// CurrentMode returns the caller's current mode. When the active major
	// mode is planner, only plan sub-agents may be spawned to prevent the
	// planner from escaping its restrictions via a coder/reviewer sub-agent.
	CurrentMode func() internal.ModeState
}

// agentToolInput is the JSON input schema for the Agent tool.
type agentToolInput struct {
	Prompt          string `json:"prompt"`
	Description     string `json:"description"`
	SubagentType    string `json:"subagent_type"`
	Resume          string `json:"resume"`
	RunInBackground bool   `json:"run_in_background"`
}

// Schema returns the tool schema.
func (t *AgentTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "agent",
		Description: "Spawn a sub-agent to execute a focused task in an isolated context. Returns the sub-agent's final result.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"prompt": map[string]any{
					"type":        "string",
					"description": "Complete task description for the sub-agent.",
				},
				"description": map[string]any{
					"type":        "string",
					"description": "Short task summary (3-5 words) for UI display.",
				},
				"subagent_type": map[string]any{
					"type":        "string",
					"description": "Mode to use: coder (default), explore, or plan.",
					"enum":        []string{"coder", "explore", "plan"},
				},
				"resume": map[string]any{
					"type":        "string",
					"description": "Optional task ID to resume an existing background agent.",
				},
				"run_in_background": map[string]any{
					"type":        "boolean",
					"description": "If true, return immediately with a task_id; result delivered later.",
				},
			},
			"required": []string{"prompt", "description"},
		},
	}
}

// Execute runs the sub-agent and returns its result.
func (t *AgentTool) Execute(input string) (string, error) {
	p, err := t.parseAndValidate(input)
	if err != nil {
		return "", err
	}
	// Resume only queries the task bus; it does not need a pool. Handle it
	// before the not-configured guard so resume of an existing task works
	// even if the pool was torn down.
	if p.Resume != "" {
		return t.resumeAgent(p.Resume)
	}

	agentType := t.resolveAgentType(p.SubagentType)
	if err := t.checkPlannerRestriction(agentType); err != nil {
		return "", err
	}
	if t.Pool == nil {
		return "", &internal.ToolError{
			Tool: "agent", Type: "not_configured",
			Detail:   "Agent pool is not available",
			HintText: "Sub-agent execution is not configured in this environment.",
		}
	}

	cfg := t.agentConfig(agentType)
	taskID := fmt.Sprintf("task-%d", time.Now().UnixNano())
	role := fmt.Sprintf("%s-%s", agentType, taskID)
	agent, err := t.Pool.CreateTaskAgent(role, cfg)
	if err != nil {
		return "", &internal.ToolError{
			Tool: "agent", Type: "spawn_failed",
			Detail:   fmt.Sprintf("Failed to create sub-agent: %v", err),
			HintText: "Check that the requested subagent_type is valid.",
		}
	}

	if p.RunInBackground {
		return t.startBackground(taskID, role, agent, p.Description, p.Prompt)
	}
	return t.runForeground(role, agent, agentType, p.Description, p.Prompt)
}

func (t *AgentTool) parseAndValidate(input string) (agentToolInput, error) {
	var p agentToolInput
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return p, &internal.ToolError{
			Tool: "agent", Type: "invalid_input",
			Detail:   fmt.Sprintf("Cannot parse parameters: %v", err),
			HintText: "Provide valid JSON with prompt and description.",
		}
	}
	if strings.TrimSpace(p.Prompt) == "" {
		return p, &internal.ToolError{
			Tool: "agent", Type: "missing_prompt",
			Detail:   "prompt is required",
			HintText: "Provide a task description in the prompt field.",
		}
	}
	if strings.TrimSpace(p.Description) == "" {
		return p, &internal.ToolError{
			Tool: "agent", Type: "missing_description",
			Detail:   "description is required",
			HintText: "Provide a short 3-5 word summary in the description field.",
		}
	}
	return p, nil
}

func (t *AgentTool) resolveAgentType(subagentType string) string {
	if subagentType == "" {
		return "coder"
	}
	return subagentType
}

func (t *AgentTool) checkPlannerRestriction(agentType string) error {
	if t.CurrentMode == nil {
		return nil
	}
	mode := t.CurrentMode()
	if mode.Major == internal.MajorPlanner && agentType != "plan" {
		return &internal.ToolError{
			Tool: "agent", Type: "forbidden_subagent",
			Detail:   fmt.Sprintf("planner mode may only spawn plan sub-agents, not %q", agentType),
			HintText: "Use subagent_type=\"plan\" or switch to a coding mode.",
		}
	}
	return nil
}

func (t *AgentTool) startBackground(taskID, role string, agent *agentic.Agent, description, prompt string) (string, error) {
	if t.TaskBus != nil {
		t.TaskBus.Register(taskID, "agent", role, description)
	}

	// Eviction happens at the end of runBackground so the one-shot agent is
	// released once the background task finishes (BUG-10).
	go t.runBackground(taskID, role, agent, prompt)
	return fmt.Sprintf("[agent] Background task started: %s\nDescription: %s", taskID, description), nil
}

func (t *AgentTool) runForeground(role string, agent *agentic.Agent, agentType, description, prompt string) (string, error) {
	// Foreground path: evict the one-shot agent once the task completes so it
	// does not accumulate in the pool (BUG-10).
	defer t.Pool.Evict(role)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	if orch := t.orchestrator(); orch != nil {
		orch.Emit("system", "user", fmt.Sprintf("Sub-agent %s started: %s", agentType, description))
	}
	result, err := agent.RunAndCollect(ctx, prompt)
	if orch := t.orchestrator(); orch != nil {
		if err != nil {
			orch.Emit("system", "user", fmt.Sprintf("Sub-agent %s failed: %v", agentType, err))
		} else {
			orch.Emit("system", "user", fmt.Sprintf("Sub-agent %s completed: %s", agentType, description))
		}
	}
	if err != nil {
		return "", &internal.ToolError{
			Tool: "agent", Type: "execution_failed",
			Detail:   fmt.Sprintf("Sub-agent failed: %v", err),
			HintText: "Retry the task or check the sub-agent configuration.",
		}
	}
	return fmt.Sprintf("[agent] %s\n\n%s", description, result), nil
}

func (t *AgentTool) orchestrator() interface {
	Emit(from, to, content string)
} {
	if t.Orchestrator != nil {
		return t.Orchestrator
	}
	if t.Pool != nil {
		if o := t.Pool.Orchestrator(); o != nil {
			return o
		}
	}
	return nil
}

func (t *AgentTool) agentConfig(agentType string) AgentConfig {
	cfg := AgentConfig{}
	if t.ModeResolver == nil {
		return cfg
	}
	major := SubagentMajorMode(agentType)
	spec, err := t.ModeResolver.Resolve(major)
	if err != nil {
		return cfg
	}
	cfg.SystemPrompt = spec.Body
	cfg.AllowedTools = spec.AllowedTools
	cfg.Temperature = spec.Temperature
	return cfg
}

// SubagentMajorMode maps the public subagent_type enum to the internal major
// mode name. 'explore' intentionally resolves to the reviewer mode (read-only
// inspection). Unknown types fall back to coder.
func SubagentMajorMode(agentType string) string {
	switch agentType {
	case "plan":
		return "planner"
	case "explore":
		return "reviewer"
	default:
		return "coder"
	}
}

func (t *AgentTool) resumeAgent(taskID string) (string, error) {
	if t.TaskBus == nil {
		return "", &internal.ToolError{
			Tool: "agent", Type: "task_not_found",
			Detail:   fmt.Sprintf("No task bus available for resume %q", taskID),
			HintText: "Task tracking is not configured in this environment.",
		}
	}
	task, ok := t.TaskBus.Get(taskID)
	if !ok {
		return "", &internal.ToolError{
			Tool: "agent", Type: "task_not_found",
			Detail:   fmt.Sprintf("No background agent with ID %q", taskID),
			HintText: "Use the task_id returned when the agent was started.",
		}
	}
	switch task.Status {
	case tasks.StatusCompleted:
		return fmt.Sprintf("[agent] Task %s completed.\n\n%s", taskID, task.Result), nil
	case tasks.StatusFailed:
		return "", &internal.ToolError{
			Tool: "agent", Type: "execution_failed",
			Detail:   fmt.Sprintf("Task %s failed: %s", taskID, task.Error),
			HintText: "The background agent reported an error; retry as a new task if needed.",
		}
	case tasks.StatusCancelled:
		return fmt.Sprintf("[agent] Task %s was cancelled.", taskID), nil
	case tasks.StatusPending, tasks.StatusRunning:
		// Still in flight: surface the current status honestly instead of a
		// fake 'will be delivered' promise. The caller can resume again later.
		return fmt.Sprintf("[agent] Task %s is still running (%s). Check back shortly.", taskID, task.Status), nil
	default:
		return fmt.Sprintf("[agent] Task %s status: %s", taskID, task.Status), nil
	}
}

func (t *AgentTool) runBackground(taskID, role string, agent *agentic.Agent, prompt string) {
	// Evict the one-shot task agent once the background run finishes (success
	// or error) so the pool does not retain it forever (BUG-10).
	defer t.Pool.Evict(role)

	if t.TaskBus != nil {
		t.TaskBus.Start(taskID)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	result, err := agent.RunAndCollect(ctx, prompt)

	if t.TaskBus != nil {
		if err != nil {
			t.TaskBus.Fail(taskID, err.Error())
		} else {
			t.TaskBus.Complete(taskID, result)
		}
	}

	if t.OnBackgroundResult != nil {
		var resultStr string
		if err != nil {
			resultStr = fmt.Sprintf("[agent error] %v", err)
		} else {
			resultStr = result
		}
		t.OnBackgroundResult(taskID, resultStr, err)
	}
}

// IsRetryable returns false — sub-agent failures are not transient.
func (t *AgentTool) IsRetryable(err error) bool { return false }

// ShortDoc returns a short documentation string.
func (t *AgentTool) ShortDoc() string { return "Execute a sub-agent for delegated tasks" }

// LongDoc returns detailed documentation.
func (t *AgentTool) LongDoc() string {
	return `Execute a sub-agent for delegated tasks with a specific goal and context.`
}

// Examples returns example inputs.
func (t *AgentTool) Examples() []string {
	return []string{
		`{"prompt": "Find all usages of fmt.Println in this repo", "description": "Find Println usages", "subagent_type": "explore"}`,
		`{"prompt": "Implement a hash table in Go", "description": "Implement hash table", "subagent_type": "coder"}`,
	}
}
