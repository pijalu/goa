// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/docs"
)

// DocEngine provides documentation lookup for commands, tools, skills, and
// embedded documentation files. Namespace prefixes:
//
//	cmd:   — commands (e.g., cmd:help, cmd:mode)
//	tool:  — tools (e.g., tool:read)
//	skill: — skills (e.g., skill:refactor)
//	docs:  — embedded docs (e.g., docs:ARCHITECTURE)
type DocEngine struct {
	registry      *CommandRegistry
	toolRegistry  ToolRegistry
	skillRegistry SkillRegistry
}

// NewDocEngine creates a new documentation engine.
func NewDocEngine(registry *CommandRegistry) *DocEngine {
	return &DocEngine{
		registry: registry,
	}
}

// SetToolRegistry attaches the tool registry for tool documentation lookups.
func (d *DocEngine) SetToolRegistry(tr ToolRegistry) {
	d.toolRegistry = tr
}

// SetSkillRegistry attaches the skill registry for skill documentation lookups.
func (d *DocEngine) SetSkillRegistry(sr SkillRegistry) {
	d.skillRegistry = sr
}

// ShortDoc returns a one-line description (≤100 chars) for the given name.
// Supports namespace prefixes: cmd:help, tool:read, skill:refactor, docs:ARCHITECTURE.
func (d *DocEngine) ShortDoc(name string) (string, error) {
	namespace, actualName := parseNamespace(name)

	switch namespace {
	case "cmd", "":
		cmd, found := d.registry.Resolve(actualName)
		if !found {
			return "", fmt.Errorf("unknown command: %s", actualName)
		}
		return cmd.ShortHelp(), nil

	case "tool":
		return toolShortDoc(actualName)

	case "skill":
		return skillShortDoc(actualName)

	case "docs":
		content, err := docs.Get(actualName)
		if err != nil {
			return "", fmt.Errorf("documentation not found: %s", actualName)
		}
		return summarize(firstLine(content)), nil

	default:
		return "", fmt.Errorf("unknown namespace: %s (use cmd:, tool:, skill:, or docs:)", namespace)
	}
}

// LongDoc returns a detailed multi-line description with examples for the
// given name. Supports the same namespace prefixes as ShortDoc.
func (d *DocEngine) LongDoc(name string) (string, error) {
	namespace, actualName := parseNamespace(name)

	switch namespace {
	case "cmd", "":
		cmd, found := d.registry.Resolve(actualName)
		if !found {
			return "", fmt.Errorf("unknown command: %s", actualName)
		}
		return cmd.LongHelp(), nil

	case "tool":
		return toolLongDoc(actualName)

	case "skill":
		return skillLongDoc(actualName)

	case "docs":
		content, err := docs.Get(actualName)
		if err != nil {
			return "", fmt.Errorf("documentation not found: %s", actualName)
		}
		return content, nil

	default:
		return "", fmt.Errorf("unknown namespace: %s (use cmd:, tool:, skill:, or docs:)", namespace)
	}
}

// toolShortDoc returns a short description for a tool.
func toolShortDoc(name string) (string, error) {
	switch name {
	case "read":
		return "Read file contents with line range support and binary detection", nil
	case "write":
		return "Write or overwrite a complete file", nil
	case "edit":
		return "Edit a file using pattern or line-based operations", nil
	case "search":
		return "Search file contents with regex, glob filters, and context lines", nil
	case "bash":
		return "Execute shell commands with security controls", nil
	case "ssh_bash":
		return "Execute commands on remote hosts via SSH", nil
	case "bg_exec":
		return "Manage background processes with pipe I/O", nil
	case "memento":
		return "Read/write persistent memory files", nil
	case "goa_command":
		return "Execute Goa commands from the LLM", nil
	case "run_skill":
		return "Execute a skill with a specific task", nil
	default:
		return fmt.Sprintf("Tool '%s' — see /tools for details", name), nil
	}
}

// toolLongDoc returns detailed documentation for a tool.
func toolLongDoc(name string) (string, error) {
	// Try embedded docs/TOOLS.md first for deep content
	content, err := docs.Get("TOOLS")
	if err == nil {
		// Extract the section for this tool
		section := extractSection(content, name)
		if section != "" {
			return fmt.Sprintf("# Tool: %s\n\n%s\n\n(see /docs tools? for full tool reference)", name, section), nil
		}
	}

	// Fallback to built-in descriptions
	short, _ := toolShortDoc(name)
	return short + "\n\nSee /docs TOOLS for the full tool system reference.", nil
}

// skillShortDoc returns a short description for a skill.
func skillShortDoc(name string) (string, error) {
	switch name {
	case "refactor":
		return "Refactor code for clarity, performance, and correctness", nil
	case "test-gen":
		return "Generate comprehensive unit tests for Go code", nil
	case "document":
		return "Add documentation comments and improve code readability", nil
	case "review":
		return "Analyze code for issues, bugs, and improvement opportunities", nil
	case "explain":
		return "Explain code in detail with architectural context", nil
	case "commit-msg":
		return "Generate conventional commit messages from staged changes", nil
	case "debug":
		return "Analyze and debug code issues step by step", nil
	default:
		return fmt.Sprintf("Skill '%s' — see /skills for details", name), nil
	}
}

// skillLongDoc returns detailed documentation for a skill.
func skillLongDoc(name string) (string, error) {
	content, err := docs.Get("SKILLS")
	if err == nil {
		section := extractSection(content, name)
		if section != "" {
			return fmt.Sprintf("# Skill: %s\n\n%s\n\n(see /docs skills? for full skill reference)", name, section), nil
		}
	}
	short, _ := skillShortDoc(name)
	return short + "\n\nSee /docs SKILLS for the full skills system reference.", nil
}

// parseNamespace splits a name like "cmd:help" into namespace and actual name.
func parseNamespace(name string) (namespace, actualName string) {
	parts := strings.SplitN(name, ":", 2)
	if len(parts) == 2 {
		return parts[0], parts[1]
	}
	return "", parts[0]
}

// firstLine returns the first non-empty line of a string.
func firstLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			// Strip markdown heading markers for cleaner output
			line = strings.TrimLeft(line, "# ")
			return line
		}
	}
	return s
}

// summarize truncates a string to ≤100 chars for short doc display.
func summarize(s string) string {
	if len(s) <= 100 {
		return s
	}
	return s[:97] + "..."
}

// extractSection extracts a section from markdown content by matching
// a heading that contains the given name.
func extractSection(content, name string) string {
	lines := strings.Split(content, "\n")
	inSection := false
	var sectionLines []string

	for _, line := range lines {
		if strings.HasPrefix(line, "## ") || strings.HasPrefix(line, "### ") {
			lower := strings.ToLower(line)
			query := strings.ToLower(name)
			if strings.Contains(lower, query) {
				inSection = true
				sectionLines = append(sectionLines, line)
				continue
			}
			if inSection {
				break
			}
		}
		if inSection {
			sectionLines = append(sectionLines, line)
		}
	}

	return strings.Join(sectionLines, "\n")
}
