// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skillrunner

import (
	"encoding/json"
	"fmt"
	"strings"

	agentic "github.com/pijalu/goa/internal/agentic"
)

// SkillLearner implements agentic.Tool and provides skill instructions
// without executing them. The LLM learns the skill and follows instructions
// using available tools in the parent session.
type SkillLearner struct {
	skills map[string]*Skill
}

// NewSkillLearner creates a SkillLearner from a SkillsLoader.
func NewSkillLearner(loader *SkillsLoader) *SkillLearner {
	sl := &SkillLearner{skills: make(map[string]*Skill)}
	if loader != nil {
		for _, s := range loader.Load(nil) {
			sl.skills[s.Name] = s
		}
	}
	return sl
}

// Schema implements agentic.Tool.
func (sl *SkillLearner) Schema() agentic.ToolSchema {
	names := make([]string, 0, len(sl.skills))
	for name := range sl.skills {
		names = append(names, name)
	}
	return agentic.ToolSchema{
		Name:        "learn_skill",
		Description: "Learn a skill's instructions, required tools, and input format. Call this BEFORE attempting to execute a skill. The returned instructions must be followed by making the specified tool calls. Do not generate data from memory — only report what tools return.",
		Schema: map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"skill_name": map[string]interface{}{
					"type":        "string",
					"description": "Name of the skill to learn",
					"enum":        names,
				},
				"task": map[string]interface{}{
					"type":        []string{"string", "object", "null"},
					"description": "Task to execute with the skill (can be null for skills that don't need input)",
				},
			},
			"required": []string{"skill_name"},
		},
	}
}

// Execute implements agentic.Tool. Returns the skill instructions for the LLM to follow.
func (sl *SkillLearner) Execute(input string) (string, error) {
	var params struct {
		SkillName string          `json:"skill_name"`
		Task      json.RawMessage `json:"task"`
	}
	if err := json.Unmarshal([]byte(input), &params); err != nil {
		return "", fmt.Errorf("parse learn_skill input: %w", err)
	}

	if params.SkillName == "" {
		return "", fmt.Errorf("missing required field: skill_name")
	}

	skill, ok := sl.skills[params.SkillName]
	if !ok {
		return "", fmt.Errorf("skill not found: %s", params.SkillName)
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# Skill: %s\n\n", skill.Name))
	sb.WriteString(fmt.Sprintf("## Description\n%s\n\n", skill.Description))

	if skill.InputSchema != nil {
		sb.WriteString("## Input Schema\n```json\n")
		schemaJSON, _ := json.MarshalIndent(skill.InputSchema, "", "  ")
		sb.Write(schemaJSON)
		sb.WriteString("\n```\n\n")
	}

	sb.WriteString("## Instructions\n")
	sb.WriteString(skill.Instructions)

	// Append task if provided
	if len(params.Task) > 0 {
		taskStr := string(params.Task)
		if len(taskStr) >= 2 && taskStr[0] == '"' && taskStr[len(taskStr)-1] == '"' {
			var s string
			if err := json.Unmarshal(params.Task, &s); err == nil {
				taskStr = s
			}
		}
		sb.WriteString("\n\n## Task\n")
		sb.WriteString(taskStr)
	}

	sb.WriteString("\n\n---\n")
	sb.WriteString("**CRITICAL**: You have learned this skill. Now you MUST follow the instructions above by calling the required tools. ")
	sb.WriteString("Do NOT generate data from memory. Only report what the tools return. ")
	sb.WriteString("If the instructions tell you to read a file, call the file reading tool. ")
	sb.WriteString("If the instructions tell you to set state, call the state tool. ")
	sb.WriteString("Failure to use tools is a failure to execute the skill.")

	return sb.String(), nil
}

// GenerateSkillsSection returns a markdown section listing all available skills
// for injection into the system prompt.
func (sl *SkillLearner) GenerateSkillsSection() string {
	if len(sl.skills) == 0 {
		return ""
	}

	var sb strings.Builder
	sb.WriteString("\n\n## Available Skills\n")
	sb.WriteString("**Skills are NOT tools — do NOT call them directly.** ")
	sb.WriteString("To use a skill, first call `learn_skill({\"skill_name\": \"...\"})` ")
	sb.WriteString("to get its instructions, then follow those instructions using the available tools. ")
	sb.WriteString("Do NOT generate responses from memory — only report what tools return.\n")

	for _, s := range sl.GetAllSkills() {
		sb.WriteString(fmt.Sprintf("\n- **%s**: %s", s.Name, s.Description))
	}
	sb.WriteString("\n")
	return sb.String()
}

// GetAllSkills returns all loaded skills.
func (sl *SkillLearner) GetAllSkills() []*Skill {
	skills := make([]*Skill, 0, len(sl.skills))
	for _, s := range sl.skills {
		skills = append(skills, s)
	}
	return skills
}

// IsRetryable implements agentic.Tool.
func (sl *SkillLearner) IsRetryable(err error) bool { return false }
