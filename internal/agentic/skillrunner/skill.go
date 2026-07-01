// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package skillrunner provides a skill runner that integrates with the agentic SDK.
// Skills are loaded from disk. In sub-agent mode the runner exposes a "run_skill"
// tool that spawns an isolated sub-agent. In inline mode a "learn_skill" tool
// returns instructions for the parent LLM to follow within the same session.
package skillrunner

import (
	"encoding/json"
	"fmt"
	"strings"
)

// Skill represents a loaded skill from a SKILL.md file.
type Skill struct {
	Name         string                 `json:"name"`
	Description  string                 `json:"description"`
	Instructions string                 `json:"instructions"` // Content after frontmatter
	InputSchema  map[string]interface{} `json:"input_schema,omitempty"`
	Path         string                 `json:"path"` // Directory path of the skill
	SubSkills    []*Skill               `json:"sub_skills,omitempty"`
	Tools        []string               `json:"tools,omitempty"`  // Requested parent tool names (empty = all)
	Skills       []string               `json:"skills,omitempty"` // Inherited parent skill names (empty = all)
}

// parseSkillMD parses SKILL.md content into a Skill struct.
// It uses simple string matching for YAML frontmatter (no external YAML dependency).
func parseSkillMD(content string, skillDir string) (*Skill, error) {
	lines := strings.Split(content, "\n")
	if len(lines) < 2 || !strings.HasPrefix(lines[0], "---") {
		return nil, fmt.Errorf("SKILL.md must start with --- (frontmatter start)")
	}

	closeIdx := findFrontmatterEnd(lines)
	if closeIdx == -1 {
		return nil, fmt.Errorf("SKILL.md missing closing --- for frontmatter")
	}

	frontmatter := parseFrontmatter(lines[1:closeIdx])
	if err := validateRequiredFrontmatter(frontmatter); err != nil {
		return nil, err
	}

	inputSchema, err := parseJSONMapField(frontmatter, "input-schema")
	if err != nil {
		return nil, err
	}
	tools, err := parseJSONListField(frontmatter, "tools")
	if err != nil {
		return nil, err
	}
	skills, err := parseJSONListField(frontmatter, "skills")
	if err != nil {
		return nil, err
	}

	return &Skill{
		Name:         frontmatter["name"],
		Description:  frontmatter["description"],
		Instructions: extractInstructions(lines, closeIdx),
		InputSchema:  inputSchema,
		Path:         skillDir,
		Tools:        tools,
		Skills:       skills,
	}, nil
}

func findFrontmatterEnd(lines []string) int {
	for i := 1; i < len(lines); i++ {
		if strings.TrimSpace(lines[i]) == "---" {
			return i
		}
	}
	return -1
}

func parseFrontmatter(lines []string) map[string]string {
	frontmatter := make(map[string]string)
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])
		frontmatter[key] = value
	}
	return frontmatter
}

func validateRequiredFrontmatter(frontmatter map[string]string) error {
	if frontmatter["name"] == "" {
		return fmt.Errorf("SKILL.md missing required 'name' field")
	}
	if frontmatter["description"] == "" {
		return fmt.Errorf("SKILL.md missing required 'description' field")
	}
	return nil
}

func parseJSONMapField(frontmatter map[string]string, key string) (map[string]interface{}, error) {
	raw, ok := frontmatter[key]
	if !ok || raw == "" {
		return nil, nil
	}
	var out map[string]interface{}
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("invalid %s JSON: %w", key, err)
	}
	return out, nil
}

func parseJSONListField(frontmatter map[string]string, key string) ([]string, error) {
	raw, ok := frontmatter[key]
	if !ok || raw == "" {
		return nil, nil
	}
	var out []string
	if err := json.Unmarshal([]byte(raw), &out); err != nil {
		return nil, fmt.Errorf("invalid %s JSON: %w", key, err)
	}
	return out, nil
}

func extractInstructions(lines []string, closeIdx int) string {
	instructions := strings.Join(lines[closeIdx+1:], "\n")
	return strings.TrimSpace(instructions)
}
