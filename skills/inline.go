// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skills

import "fmt"

// InlineSkillInjector injects inline skill instructions into an existing
// system prompt with ## Skill: <name> wrappers.
type InlineSkillInjector struct {
	registry *SkillRegistry
}

// NewInlineSkillInjector creates an injector from a registry.
func NewInlineSkillInjector(registry *SkillRegistry) *InlineSkillInjector {
	return &InlineSkillInjector{
		registry: registry,
	}
}

// Inject wraps inline and knowledge-type skill instructions into the system
// prompt. For each enabled inline or knowledge skill, appends:
//
//	## Skill: <name>
//	<skill instructions>
//	## End Skill
func (i *InlineSkillInjector) Inject(systemPrompt string, enabledSkills []string) string {
	result := systemPrompt
	for _, name := range enabledSkills {
		skill, ok := i.registry.Get(name)
		if !ok {
			continue
		}
		// Inject if the skill is inline or has category "knowledge".
		if !skill.Meta.Inline && categoryOrDefault(skill.Meta.Category) != SkillCategoryKnowledge {
			continue
		}
		result += fmt.Sprintf("\n\n## Skill: %s\n%s\n## End Skill", name, StripSkillNoise(skill.Body))
	}
	return result
}
