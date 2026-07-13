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
		return d.toolShortDoc(actualName)

	case "skill":
		return d.skillShortDoc(actualName)

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
		return d.toolLongDoc(actualName)

	case "skill":
		return d.skillLongDoc(actualName)

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

// toolShortDoc returns a short description for a tool, preferring the
// tool's own embedded short.md via the registry, falling back to
// docs/TOOLS.md section extraction, then to a generic placeholder.
func (d *DocEngine) toolShortDoc(name string) (string, error) {
	// 1. Prefer the tool's own embedded short.md via the registry.
	if d.toolRegistry != nil {
		if tool, ok := d.toolRegistry.Get(name); ok {
			if sd, ok := tool.(interface{ ShortDoc() string }); ok {
				if doc := sd.ShortDoc(); doc != "" {
					return doc, nil
				}
			}
			// Fallback to Schema().Description if not documentable.
			if desc := tool.Schema().Description; desc != "" {
				return desc, nil
			}
		}
	}

	// 2. Try embedded docs/TOOLS.md section extraction.
	if content, err := docs.Get("TOOLS"); err == nil {
		if section := extractSection(content, name); section != "" {
			return summarize(firstLine(section)), nil
		}
	}

	return fmt.Sprintf("Tool '%s' — see /tools for details", name), nil
}

// toolLongDoc returns detailed documentation for a tool, preferring
// the tool's own embedded long.md, then docs/TOOLS.md section extraction.
func (d *DocEngine) toolLongDoc(name string) (string, error) {
	// 1. Prefer the tool's own embedded long.md via the registry.
	if d.toolRegistry != nil {
		if tool, ok := d.toolRegistry.Get(name); ok {
			if ld, ok := tool.(interface{ LongDoc() string }); ok {
				if doc := ld.LongDoc(); doc != "" {
					return fmt.Sprintf("# Tool: %s\n\n%s\n\n(see /docs tools? for full tool reference)", name, doc), nil
				}
			}
			// Fallback to embedded short.md.
			if sd, ok := tool.(interface{ ShortDoc() string }); ok {
				if doc := sd.ShortDoc(); doc != "" {
					return doc + "\n\nSee /docs TOOLS for the full tool system reference.", nil
				}
			}
		}
	}

	// 2. Try embedded docs/TOOLS.md section extraction.
	content, err := docs.Get("TOOLS")
	if err == nil {
		if section := extractSection(content, name); section != "" {
			return fmt.Sprintf("# Tool: %s\n\n%s\n\n(see /docs tools? for full tool reference)", name, section), nil
		}
	}

	// 3. Generic fallback.
	return fmt.Sprintf("Tool '%s' — see /tools for details", name), nil
}

// skillShortDoc returns a short description for a skill, preferring the
// skill's embedded SKILL.md frontmatter description via the registry.
func (d *DocEngine) skillShortDoc(name string) (string, error) {
	if d.skillRegistry != nil {
		if skill, ok := d.skillRegistry.Get(name); ok {
			if desc := skill.Meta.Description; desc != "" {
				return desc, nil
			}
		}
	}

	// Fallback: try embedded docs/SKILLS.md section extraction.
	if content, err := docs.Get("SKILLS"); err == nil {
		if section := extractSection(content, name); section != "" {
			return summarize(firstLine(section)), nil
		}
	}

	return fmt.Sprintf("Skill '%s' — see /skills for details", name), nil
}

// skillLongDoc returns detailed documentation for a skill, preferring
// the skill's embedded SKILL.md body via the registry, then docs/SKILLS.md.
func (d *DocEngine) skillLongDoc(name string) (string, error) {
	if d.skillRegistry != nil {
		if skill, ok := d.skillRegistry.Get(name); ok {
			if body := skill.Body; body != "" {
				return fmt.Sprintf("# Skill: %s\n\n%s\n\n(see /docs skills? for full skill reference)", name, body), nil
			}
		}
	}

	// Fallback: try embedded docs/SKILLS.md section extraction.
	content, err := docs.Get("SKILLS")
	if err == nil {
		if section := extractSection(content, name); section != "" {
			return fmt.Sprintf("# Skill: %s\n\n%s\n\n(see /docs skills? for full skill reference)", name, section), nil
		}
	}

	return fmt.Sprintf("Skill '%s' — see /skills for details", name), nil
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
