// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skills

import (
	"strings"
)

// PromptRenderer loads and renders prompt templates.
// Implementations use the prompts.Registry which checks user directories
// before falling back to embedded defaults.
type PromptRenderer interface {
	Render(name string, data interface{}) (string, error)
}

// ToSkillSummaries converts a list of Skills to SkillSummaries for prompt inclusion.
func ToSkillSummaries(skills []*Skill) []SkillSummary {
	out := make([]SkillSummary, 0, len(skills))
	for _, s := range skills {
		out = append(out, SkillSummary{
			Name:        s.Meta.Name,
			Description: s.Meta.Description,
			FilePath:    s.FilePath,
			Inline:      s.Meta.Inline,
			Category:    categoryOrDefault(s.Meta.Category),
		})
	}
	return out
}

// RenderAvailableSkills renders the <available_skills> prompt via the given
// renderer. Returns empty string if no skills or if the renderer fails.
func RenderAvailableSkills(renderer PromptRenderer, skills []SkillSummary) string {
	if len(skills) == 0 || renderer == nil {
		return ""
	}
	result, err := renderer.Render("available_skills", escapeSkills(skills))
	if err != nil {
		return ""
	}
	return result
}

// RenderSkillShow renders the /skill:name? display via the given renderer.
func RenderSkillShow(renderer PromptRenderer, skill *Skill) string {
	if renderer == nil {
		return ""
	}
	result, err := renderer.Render("skill_show", map[string]any{
		"Name":        escapeXML(skill.Meta.Name),
		"Description": escapeXML(skill.Meta.Description),
		"Source":      skill.Source,
		"FilePath":    escapeXML(skill.FilePath),
		"Inline":      skill.Meta.Inline,
		"Category":    categoryOrDefault(skill.Meta.Category),
		"Mode":        skill.Meta.Mode,
		"Body":        skill.Body,
	})
	if err != nil {
		return ""
	}
	return result
}

// RenderSkillExpand renders the /skill:name expansion via the given renderer.
func RenderSkillExpand(renderer PromptRenderer, skill *Skill, args string) string {
	if renderer == nil {
		return ""
	}
	result, err := renderer.Render("skill_expand", map[string]any{
		"Name":     escapeXML(skill.Meta.Name),
		"FilePath": escapeXML(skill.FilePath),
		"Body":     skill.Body,
		"Args":     args,
	})
	if err != nil {
		return ""
	}
	return result
}

// RenderSkillToolResult renders the run_skill tool result via the given renderer.
func RenderSkillToolResult(renderer PromptRenderer, skillName, mode, output string) string {
	if renderer == nil {
		return ""
	}
	templateName := "skill_inline_result"
	if mode == "sub-agent" {
		templateName = "skill_subagent_result"
	}
	result, err := renderer.Render(templateName, map[string]string{
		"SkillName": skillName,
		"Output":    output,
	})
	if err != nil {
		return ""
	}
	return result
}

type safeSkill struct {
	Name        string
	Description string
	Category    string
	FilePath    string
}

func escapeSkills(skills []SkillSummary) []safeSkill {
	out := make([]safeSkill, len(skills))
	for i, s := range skills {
		out[i] = safeSkill{
			Name:        escapeXML(s.Name),
			Description: escapeXML(s.Description),
			Category:    escapeXML(s.Category),
			FilePath:    escapeXML(s.FilePath),
		}
	}
	return out
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}
