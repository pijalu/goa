// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skills

import (
	"fmt"
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
			// Invocation policy defaults are applied by SkillMeta.UnmarshalYAML
			// (both true when omitted) for YAML-parsed skills.
			ModelInvocable: s.Meta.ModelInvocable,
			UserInvocable:  s.Meta.UserInvocable,
			Hidden:         s.Meta.Hidden,
		})
	}
	return out
}

// availableSkillsBudget bounds the bytes spent listing individual skills in
// <available_skills> so a workspace with an extreme number of skills can't
// bloat every turn's prompt. Skills past the budget are summarized as a count
// (…and N more) rather than dropped silently — the model can still load any of
// them by name, and an unknown name returns the full list from run_skill. The
// first skill is always listed, so a skill set is never rendered as empty.
const availableSkillsBudget = 4096

// maxSkillDescriptionRunes caps each skill's one-line description so a single
// verbose description can't dominate the skills-list budget. The cap is roomy
// on purpose: the description carries the skill's trigger conditions ("use
// when…"), and truncating those is what stops the model from ever invoking the
// skill.
const maxSkillDescriptionRunes = 200

// availableSkillsData is the template payload for the <available_skills>
// prompt section: the skills plus the mode-dependent header line and, when the
// catalog is over-budget, a count-summary line.
type availableSkillsData struct {
	Header  string
	Skills  []safeSkill
	Summary string
}

// RenderAvailableSkills renders the <available_skills> prompt via the given
// renderer. Returns empty string if no skills or if the renderer fails.
// runSkillAvailable must reflect whether the run_skill tool is actually
// registered; when it is not (inline execution mode), action skills are
// advertised with their /skill:run:<name> invocation instead of a
// nonexistent tool.
//
// The listing is the model-facing skill catalog: skills that are not
// model-invocable (model_invocable:false, or user_invocable:false per the
// P16 acceptance that a user-non-invocable skill never appears in the
// model's tool schema) are filtered out before rendering.
//
// The catalog is budgeted (availableSkillsBudget): descriptions are truncated
// to maxSkillDescriptionRunes, and skills past the budget are summarized as a
// count instead of being dropped silently.
func RenderAvailableSkills(renderer PromptRenderer, skills []SkillSummary, runSkillAvailable bool) string {
	if len(skills) == 0 || renderer == nil {
		return ""
	}
	skills = filterModelInvocable(skills)
	if len(skills) == 0 {
		return ""
	}
	safe := escapeSkills(skills, runSkillAvailable)
	listed, omitted := budgetSkills(safe)
	data := availableSkillsData{
		Header:  availableSkillsHeader(runSkillAvailable),
		Skills:  listed,
		Summary: availableSkillsSummary(omitted, runSkillAvailable),
	}
	result, err := renderer.Render("available_skills", data)
	if err != nil {
		return ""
	}
	return result
}

// budgetSkills keeps the first skill unconditionally (never silently drop a
// skill set to zero), then lists skills while their rendered lines fit
// availableSkillsBudget; the rest are counted as omitted so the caller can
// summarize them. Line costs are computed on the XML-escaped fields the
// template actually renders, matching the byte cost of the prompt.
func budgetSkills(skills []safeSkill) (listed []safeSkill, omitted int) {
	spent := 0
	for i, s := range skills {
		// The embedded template emits "\n  <skill …>…</skill>\n" per skill.
		line := 4 + len(skillLine(s))
		if i > 0 && spent+line > availableSkillsBudget {
			omitted++
			continue
		}
		listed = append(listed, s)
		spent += line
	}
	return listed, omitted
}

// skillLine mirrors the per-skill line of the embedded available_skills
// template so budgetSkills can account for exactly what gets rendered.
func skillLine(s safeSkill) string {
	return `<skill name="` + s.Name + `" category="` + s.Category + `" tool="` + s.ExecuteTool + `" location="` + s.FilePath + `">` + s.Description + `</skill>`
}

// availableSkillsSummary renders the count-summary line for skills past the
// budget. It is mode-aware like the header: with run_skill registered the
// model calls it by name; without it (inline execution mode) the model
// invokes /skill:run:<name>.
func availableSkillsSummary(omitted int, runSkillAvailable bool) string {
	if omitted <= 0 {
		return ""
	}
	if runSkillAvailable {
		return fmt.Sprintf("…and %d more (call run_skill with a name; unknown names list all)", omitted)
	}
	return fmt.Sprintf("…and %d more (invoke with /skill:run:<name>; unknown names list all)", omitted)
}

// filterModelInvocable returns only the skills the model may invoke,
// preserving input order.
func filterModelInvocable(skills []SkillSummary) []SkillSummary {
	out := make([]SkillSummary, 0, len(skills))
	for _, s := range skills {
		if s.IsModelInvocable() {
			out = append(out, s)
		}
	}
	return out
}

// availableSkillsHeader returns the header line describing how each skill
// category is invoked in the current execution mode.
func availableSkillsHeader(runSkillAvailable bool) string {
	if runSkillAvailable {
		return "Action skills: run_skill. Inline/knowledge: read."
	}
	return "Action skills: invoke with the /skill:run:<name> command. Inline/knowledge: read."
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
	Name             string
	Description      string
	Category         string
	FilePath         string
	ExecuteTool      string
	RequiresSubAgent bool
}

func escapeSkills(skills []SkillSummary, runSkillAvailable bool) []safeSkill {
	out := make([]safeSkill, len(skills))
	for i, s := range skills {
		executeTool := "read"
		if s.RequiresSubAgent || categoryOrDefault(s.Category) == SkillCategoryAction {
			if runSkillAvailable {
				executeTool = "run_skill"
			} else {
				executeTool = "/skill:run:" + s.Name
			}
		}
		out[i] = safeSkill{
			Name:             escapeXML(s.Name),
			Description:      escapeXML(truncateSkillDescription(s.Description)),
			Category:         escapeXML(s.Category),
			FilePath:         escapeXML(s.FilePath),
			ExecuteTool:      executeTool,
			RequiresSubAgent: s.RequiresSubAgent,
		}
	}
	return out
}

// truncateSkillDescription keeps a skill's one-line description short so a
// single verbose description can't dominate the skills-list budget. The
// ellipsis is reserved inside the cap, so the result is at most
// maxSkillDescriptionRunes runes.
func truncateSkillDescription(desc string) string {
	runes := []rune(desc)
	if len(runes) <= maxSkillDescriptionRunes {
		return desc
	}
	return strings.TrimSpace(string(runes[:maxSkillDescriptionRunes-1])) + "…"
}

func escapeXML(s string) string {
	s = strings.ReplaceAll(s, "&", "&amp;")
	s = strings.ReplaceAll(s, "<", "&lt;")
	s = strings.ReplaceAll(s, ">", "&gt;")
	s = strings.ReplaceAll(s, "\"", "&quot;")
	s = strings.ReplaceAll(s, "'", "&apos;")
	return s
}
