// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skills

import (
	"strings"
)

const skillPrefix = "/skill:"

// ParseSkillCommand parses a /skill:name[?] [args] input and returns the
// skill name, whether help was requested, and the optional args.
// Returns ("", false, "") if the input is not a skill command.
//
// Grammar:
//
//	/skill:name       → execute bare (no args)
//	/skill:name args  → execute with task args
//	/skill:name?      → show skill content (help mode, args ignored)
func ParseSkillCommand(input string) (name string, showHelp bool, args string) {
	if !strings.HasPrefix(input, skillPrefix) {
		return "", false, ""
	}
	rest := input[len(skillPrefix):]

	// Check for trailing ? (must be glued to name, no space before ?)
	if idx := strings.Index(rest, "?"); idx >= 0 {
		name = rest[:idx]
		showHelp = true
		return name, showHelp, ""
	}

	// Split on first space for args
	if spaceIdx := strings.Index(rest, " "); spaceIdx >= 0 {
		return rest[:spaceIdx], false, rest[spaceIdx+1:]
	}

	return rest, false, ""
}

// ExpandSkillCommand expands a /skill:name input via the given renderer.
// Returns the expanded text, or the original input if the skill is not found.
//
// For /skill:name? (help mode): renders the skill_show template.
// For /skill:name [args] (execution): renders the skill_expand template.
func ExpandSkillCommand(renderer PromptRenderer, registry *SkillRegistry, input string) string {
	name, showHelp, args := ParseSkillCommand(input)
	if name == "" {
		return input // not a skill command, pass through
	}

	skill, ok := registry.Get(name)
	if !ok {
		return input // unknown skill, pass through
	}

	if showHelp {
		result := RenderSkillShow(renderer, skill)
		if result != "" {
			return result
		}
		return input
	}

	result := RenderSkillExpand(renderer, skill, args)
	if result != "" {
		return result
	}
	return input
}
