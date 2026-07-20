// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skills

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/multiagent"
)

// SkillRunnerTool wraps skill execution as an agentic.Tool.
//
// In sub-agent execution mode it spawns a sub-agent via AgentPool with the
// skill body as system prompt and returns the collected result.
//
// In inline execution mode it returns the skill body (plus any sub-skills and
// imports) as the tool result, so the calling agent follows the instructions
// with its own tools — no sub-agent is spawned.
//
// Knowledge/inline skills do NOT need this tool — they work via
// <available_skills> XML in the system prompt, where the LLM is told to use
// the read tool to load the SKILL.md file on demand.
type SkillRunnerTool struct {
	Registry *SkillRegistry
	Pool     *multiagent.AgentPool // required in sub-agent mode; unused inline
	Renderer PromptRenderer        // for rendering tool result templates
	// Inline reports whether skills execute inline (tool result returns the
	// skill instructions) instead of spawning a sub-agent.
	Inline bool
}

// NewSkillRunnerTool creates a skill runner tool for the given execution mode.
func NewSkillRunnerTool(registry *SkillRegistry, pool *multiagent.AgentPool, renderer PromptRenderer, inline bool) *SkillRunnerTool {
	return &SkillRunnerTool{
		Registry: registry,
		Pool:     pool,
		Renderer: renderer,
		Inline:   inline,
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

// Execute runs a skill with the given input. In inline mode the skill
// instructions are returned as the tool result; in sub-agent mode a
// dedicated sub-agent executes the skill and its output is returned.
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

	if t.Inline {
		return t.executeInline(skill, task), nil
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

// executeInline returns the skill body (plus sub-skills and imports) and the
// task as the tool result. The calling agent then follows the instructions
// using its own tools within the same session.
func (t *SkillRunnerTool) executeInline(skill *Skill, task string) string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("# Skill: %s\n\n", skill.Meta.Name))
	b.WriteString(skill.Body)
	if subs := t.Registry.SubSkills(skill.Meta.Name); len(subs) > 0 {
		b.WriteString("\n\n## Sub-skills\n")
		for _, sub := range subs {
			b.WriteString(fmt.Sprintf("\n### %s\n%s\n", sub.Meta.Name, sub.Body))
		}
	}
	if imports := t.Registry.ImportedSkills(skill.Meta.Name); len(imports) > 0 {
		b.WriteString("\n\n## Imported skills\n")
		for _, imp := range imports {
			b.WriteString(fmt.Sprintf("\n### %s\n%s\n", imp.Meta.Name, imp.Body))
		}
	}
	b.WriteString(fmt.Sprintf("\n\n## Task\n%s\n", task))
	b.WriteString("\nFollow the skill instructions above and complete the task using available tools.")
	if t.Renderer != nil {
		if rendered := RenderSkillToolResult(t.Renderer, skill.Meta.Name, "inline", b.String()); rendered != "" {
			return rendered
		}
	}
	return b.String()
}

// runSubAgent spawns a sub-agent via AgentPool with the skill body as its
// system prompt, runs the task, and returns the collected output.
func (t *SkillRunnerTool) runSubAgent(skill *Skill, task string) (string, error) {
	if t.Pool == nil {
		return "", fmt.Errorf("sub-agent execution requires AgentPool (not configured)")
	}

	roleName := fmt.Sprintf("skill-%s-%d", skill.Meta.Name, time.Now().UnixNano())
	allowedTools := t.resolveAllowedTools(skill)
	systemPrompt, userPrompt := t.buildSubAgentPrompt(skill, task)

	agent, err := t.Pool.CreateTaskAgent(roleName, multiagent.AgentConfig{
		SystemPrompt:    systemPrompt,
		AllowedTools:    allowedTools,
		ReasoningEffort: agentic.ReasoningEffortOff,
	})
	if err != nil {
		return "", fmt.Errorf("create skill sub-agent: %w", err)
	}
	defer t.Pool.Evict(roleName)

	result, err := agent.RunAndCollect(context.Background(), userPrompt)
	if err != nil {
		return "", fmt.Errorf("skill sub-agent failed: %w", err)
	}
	return result, nil
}

func (t *SkillRunnerTool) resolveAllowedTools(skill *Skill) []string {
	all := t.Pool.ToolNames()
	allowed := skill.Meta.Tools
	if len(allowed) == 0 {
		allowed = defaultSubAgentToolsFrom(all)
	}
	return excludeToolNames(allowed, "run_skill", "terminal")
}

func defaultSubAgentToolsFrom(all []string) []string {
	candidates := map[string]bool{
		"bash":     true,
		"read":     true,
		"edit":     true,
		"write":    true,
		"webfetch": true,
	}
	var out []string
	for _, name := range all {
		if candidates[name] {
			out = append(out, name)
		}
	}
	return out
}

func excludeToolNames(names []string, excluded ...string) []string {
	ex := make(map[string]bool, len(excluded))
	for _, e := range excluded {
		ex[e] = true
	}
	var out []string
	for _, n := range names {
		if !ex[n] {
			out = append(out, n)
		}
	}
	return out
}

func (t *SkillRunnerTool) buildSubAgentPrompt(skill *Skill, task string) (string, string) {
	systemPrompt := "You are a skill executor. Execute the instructions in the user message and return the final output. Do not plan, summarize, or explain the instructions; perform the work immediately. Use the bash tool for shell commands. Return only the final output."
	var b strings.Builder
	b.WriteString(fmt.Sprintf("[Skill: %s]\n%s\n", skill.Meta.Name, skill.Body))
	if subs := t.Registry.SubSkills(skill.Meta.Name); len(subs) > 0 {
		b.WriteString("\n## Sub-skills\n")
		for _, sub := range subs {
			b.WriteString(fmt.Sprintf("\n### %s\n%s\n", sub.Meta.Name, sub.Body))
		}
	}
	if imports := t.Registry.ImportedSkills(skill.Meta.Name); len(imports) > 0 {
		b.WriteString("\n## Imported skills\n")
		for _, imp := range imports {
			b.WriteString(fmt.Sprintf("\n### %s\n%s\n", imp.Meta.Name, imp.Body))
		}
	}
	if task == "" {
		task = "Run the commands in the skill instructions and return the raw output. Do not plan or explain."
	}
	b.WriteString(fmt.Sprintf("\nTask: %s\n", task))
	return systemPrompt, b.String()
}

// IsRetryable returns false — tool errors are deterministic.
func (t *SkillRunnerTool) IsRetryable(err error) bool { return false }
