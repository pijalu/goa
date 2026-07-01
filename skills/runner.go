// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skills

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/multiagent"
)

// SkillRunnerTool wraps sub-agent skill execution as an agentic.Tool.
// Inline skills do NOT use this tool — they work via <available_skills> XML
// in the system prompt, where the LLM is told to use the read tool to load
// the SKILL.md file on demand.
//
// Only sub-agent skills are executed through this tool: it spawns a sub-agent
// via AgentPool with the skill body as system prompt and returns the result.
type SkillRunnerTool struct {
	Registry *SkillRegistry
	Pool     *multiagent.AgentPool // required
	Renderer PromptRenderer        // for rendering tool result templates
}

// NewSkillRunnerTool creates a skill runner tool for sub-agent execution.
func NewSkillRunnerTool(registry *SkillRegistry, pool *multiagent.AgentPool, renderer PromptRenderer) *SkillRunnerTool {
	return &SkillRunnerTool{
		Registry: registry,
		Pool:     pool,
		Renderer: renderer,
	}
}

// Schema returns the tool schema for run_skill.
func (t *SkillRunnerTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{
		Name:        "run_skill",
		Description: "Execute a skill with a specific task. Skills provide specialized capabilities like refactoring, test generation, documentation, and more.",
		Schema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"skill_name": map[string]any{
					"type":        "string",
					"description": "Name of the skill to execute (e.g., refactor, test-gen, document, review, explain)",
				},
				"task": map[string]any{
					"type":        "string",
					"description": "Description of the task for the skill to accomplish",
				},
			},
			"required": []string{"skill_name", "task"},
		},
	}
}

// skillRunParams holds the parsed input for SkillRunnerTool.
type skillRunParams struct {
	SkillName string `json:"skill_name"`
	Task      string `json:"task"`
}

// Execute runs a skill with the given input in sub-agent mode.
func (t *SkillRunnerTool) Execute(input string) (string, error) {
	var p skillRunParams
	if err := json.Unmarshal([]byte(input), &p); err != nil {
		return "", fmt.Errorf("invalid input: %v", err)
	}
	skillName := p.SkillName
	task := p.Task

	if skillName == "" {
		return "", fmt.Errorf("skill_name is required")
	}
	if task == "" {
		return "", fmt.Errorf("task is required")
	}

	skill, ok := t.Registry.Get(skillName)
	if !ok {
		return "", fmt.Errorf("skill %q not found — use /skills to list available skills", skillName)
	}

	output, err := t.runSubAgent(skill, task)
	if err != nil {
		return "", err
	}

	// Render via template if renderer is available
	if t.Renderer != nil {
		if rendered := RenderSkillToolResult(t.Renderer, skill.Meta.Name, "sub-agent", output); rendered != "" {
			return rendered, nil
		}
	}
	return output, nil
}

// runSubAgent spawns a sub-agent via AgentPool with the skill body as its
// system prompt, runs the task, and returns the collected output.
func (t *SkillRunnerTool) runSubAgent(skill *Skill, task string) (string, error) {
	if t.Pool == nil {
		return "", fmt.Errorf("sub-agent execution requires AgentPool (not configured)")
	}

	roleName := "skill-" + skill.Meta.Name
	t.Pool.SetConfig(roleName, multiagent.AgentConfig{
		SystemPrompt: skill.Body,
	})

	agent, err := t.Pool.GetOrCreate(roleName)
	if err != nil {
		return "", fmt.Errorf("create skill sub-agent: %w", err)
	}

	// TODO: thread the agent's run context through tool execution so this
	// can be cancelled mid-flight. For now, context.Background() mirrors the
	// previous behaviour.
	result, err := agent.RunAndCollect(context.Background(), "Task: "+task)
	if err != nil {
		return "", fmt.Errorf("skill sub-agent failed: %w", err)
	}

	return result, nil
}

// IsRetryable returns false — tool errors are deterministic.
func (t *SkillRunnerTool) IsRetryable(err error) bool { return false }
