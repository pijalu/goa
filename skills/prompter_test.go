// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package skills

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/prompts"
)

type fakeRenderer struct {
	lastData interface{}
	rendered string
}

func (f *fakeRenderer) Render(name string, data interface{}) (string, error) {
	f.lastData = data
	return f.rendered, nil
}

func TestRenderAvailableSkills_ExecuteToolPerCategory(t *testing.T) {
	fr := &fakeRenderer{rendered: "rendered"}
	skills := []SkillSummary{
		{Name: "inline", Description: "Inline", Category: "knowledge", ModelInvocable: true, UserInvocable: true},
		{Name: "action", Description: "Action", Category: "action", ModelInvocable: true, UserInvocable: true},
		{Name: "sub", Description: "Sub", RequiresSubAgent: true, ModelInvocable: true, UserInvocable: true},
	}
	_ = RenderAvailableSkills(fr, skills, true)
	if fr.lastData == nil {
		t.Fatal("expected renderer to receive data")
	}
	rendered, ok := fr.lastData.(availableSkillsData)
	if !ok {
		t.Fatalf("expected availableSkillsData, got %T", fr.lastData)
	}
	want := map[string]string{
		"inline": "read",
		"action": "run_skill",
		"sub":    "run_skill",
	}
	for _, s := range rendered.Skills {
		if s.ExecuteTool != want[s.Name] {
			t.Errorf("%s ExecuteTool = %q, want %q", s.Name, s.ExecuteTool, want[s.Name])
		}
	}
}

// When the run_skill tool is not registered (inline execution mode), action
// skills must not be advertised with tool="run_skill" — the model would call
// a nonexistent tool. They are invocable via the /skill:run:<name> command.
func TestRenderAvailableSkills_NoRunSkillTool(t *testing.T) {
	fr := &fakeRenderer{rendered: "rendered"}
	skills := []SkillSummary{
		{Name: "inline", Description: "Inline", Category: "knowledge", ModelInvocable: true, UserInvocable: true},
		{Name: "action", Description: "Action", Category: "action", ModelInvocable: true, UserInvocable: true},
		{Name: "sub", Description: "Sub", RequiresSubAgent: true, ModelInvocable: true, UserInvocable: true},
	}
	_ = RenderAvailableSkills(fr, skills, false)
	rendered, ok := fr.lastData.(availableSkillsData)
	if !ok {
		t.Fatalf("expected availableSkillsData, got %T", fr.lastData)
	}
	for _, s := range rendered.Skills {
		if s.ExecuteTool == "run_skill" {
			t.Errorf("%s: must not advertise run_skill when the tool is unavailable", s.Name)
		}
	}
	want := map[string]string{
		"inline": "read",
		"action": "/skill:run:action",
		"sub":    "/skill:run:sub",
	}
	for _, s := range rendered.Skills {
		if s.ExecuteTool != want[s.Name] {
			t.Errorf("%s ExecuteTool = %q, want %q", s.Name, s.ExecuteTool, want[s.Name])
		}
	}
}

func TestEscapeSkills_XMLEscaping(t *testing.T) {
	skills := []SkillSummary{
		{Name: "a&b", Description: "x<y", Category: "action"},
	}
	out := escapeSkills(skills, true)
	if len(out) != 1 {
		t.Fatal("expected one safe skill")
	}
	if !strings.Contains(out[0].Name, "&amp;") {
		t.Errorf("expected XML-escaped name, got %q", out[0].Name)
	}
	if !strings.Contains(out[0].Description, "&lt;") {
		t.Errorf("expected XML-escaped description, got %q", out[0].Description)
	}
}

// TestRenderAvailableSkills_FiltersModelInvocable is the P16 acceptance that
// the model-facing <available_skills> catalog excludes skills the model
// cannot invoke: model_invocable:false, and (per the acceptance) any
// user_invocable:false skill.
func TestRenderAvailableSkills_FiltersModelInvocable(t *testing.T) {
	fr := &fakeRenderer{rendered: "rendered"}
	skills := []SkillSummary{
		{Name: "plain", Description: "P", Category: "action", ModelInvocable: true, UserInvocable: true},
		{Name: "model-off", Description: "M", Category: "action", ModelInvocable: false, UserInvocable: true},
		{Name: "user-off", Description: "U", Category: "action", ModelInvocable: true, UserInvocable: false},
	}
	_ = RenderAvailableSkills(fr, skills, true)
	rendered, ok := fr.lastData.(availableSkillsData)
	if !ok {
		t.Fatalf("expected availableSkillsData, got %T", fr.lastData)
	}
	got := map[string]bool{}
	for _, s := range rendered.Skills {
		got[s.Name] = true
	}
	if !got["plain"] {
		t.Errorf("plain skill should be advertised to the model: %v", got)
	}
	if got["model-off"] {
		t.Errorf("model_invocable:false skill must not be advertised to the model: %v", got)
	}
	if got["user-off"] {
		t.Errorf("user_invocable:false skill must not be advertised to the model (P16 acceptance): %v", got)
	}
}

// TestBudgetSkills_CountsOmitted is the P5 budget-accounting unit test: with
// a pathological skill set the first skill is always listed (never silently
// drop to zero), the remaining skills are counted for the summary, and the
// listed lines fit the byte budget.
func TestBudgetSkills_CountsOmitted(t *testing.T) {
	var skills []safeSkill
	for i := 0; i < 80; i++ {
		skills = append(skills, safeSkill{
			Name:        fmt.Sprintf("s%02d", i),
			Category:    "action",
			ExecuteTool: "run_skill",
			FilePath:    "/skills/" + fmt.Sprintf("s%02d", i) + "/SKILL.md",
			Description: strings.Repeat("a", maxSkillDescriptionRunes),
		})
	}
	listed, omitted := budgetSkills(skills)
	if len(listed) == 0 {
		t.Fatal("must always list at least one skill")
	}
	if len(listed)+omitted != len(skills) {
		t.Errorf("listed+omitted = %d, want %d (no silent drops)", len(listed)+omitted, len(skills))
	}
	if omitted == 0 {
		t.Errorf("expected skills past the budget to be summarized, listed=%d", len(listed))
	}
	spent := 0
	for _, s := range listed {
		spent += 4 + len(skillLine(s))
	}
	if spent > availableSkillsBudget {
		t.Errorf("listed lines = %d bytes, budget = %d", spent, availableSkillsBudget)
	}
}

// TestRenderAvailableSkills_BudgetIntegration is the P5 acceptance rendered
// through the real embedded template: a pathological skill set stays within
// the byte budget (plus fixed header/wrapper/summary overhead), the
// count-summary line is present, and every listed description is at most
// maxSkillDescriptionRunes runes.
func TestRenderAvailableSkills_BudgetIntegration(t *testing.T) {
	reg := prompts.NewRegistry(prompts.EmbeddedFS())

	var skills []SkillSummary
	long := strings.Repeat("use when the request is about xyz ", 12) // 288 runes
	for i := 0; i < 120; i++ {
		skills = append(skills, SkillSummary{
			Name:           fmt.Sprintf("skill-%03d", i),
			Description:    long,
			Category:       "action",
			FilePath:       fmt.Sprintf("/skills/skill-%03d/SKILL.md", i),
			ModelInvocable: true,
			UserInvocable:  true,
		})
	}

	rendered := RenderAvailableSkills(reg, skills, true)
	if rendered == "" {
		t.Fatal("expected rendered catalog")
	}
	if !strings.Contains(rendered, "…and ") {
		t.Errorf("expected count-summary line, got:\n%s", rendered)
	}
	if !strings.Contains(rendered, "unknown names list all") {
		t.Errorf("expected summary to point at the discovery path, got:\n%s", rendered)
	}

	// Every listed skill line's description must be ≤ maxSkillDescriptionRunes.
	for _, line := range strings.Split(rendered, "\n") {
		if !strings.Contains(line, "<skill ") {
			continue
		}
		desc := extractSkillDescription(t, line)
		if len([]rune(desc)) > maxSkillDescriptionRunes {
			t.Errorf("description exceeds %d runes (%d): %q", maxSkillDescriptionRunes, len([]rune(desc)), desc)
		}
	}

	// The skill lines respect the budget; header + wrapper + summary are
	// fixed overhead (bounded well under 512 bytes).
	if len(rendered) > availableSkillsBudget+512 {
		t.Errorf("catalog rendering = %d bytes, want ≤ %d (+overhead)", len(rendered), availableSkillsBudget)
	}
}

// TestRenderAvailableSkills_BudgetSummaryMode verifies the summary line is
// mode-aware: with run_skill registered it points at run_skill; in inline
// mode it points at /skill:run:<name>.
func TestRenderAvailableSkills_BudgetSummaryMode(t *testing.T) {
	long := strings.Repeat("x", 300)
	var skills []SkillSummary
	for i := 0; i < 40; i++ {
		skills = append(skills, SkillSummary{
			Name:           fmt.Sprintf("s%02d", i),
			Description:    long,
			Category:       "action",
			ModelInvocable: true,
			UserInvocable:  true,
		})
	}

	fr := &fakeRenderer{rendered: "rendered"}
	_ = RenderAvailableSkills(fr, skills, true)
	data, ok := fr.lastData.(availableSkillsData)
	if !ok {
		t.Fatalf("expected availableSkillsData, got %T", fr.lastData)
	}
	if data.Summary == "" {
		t.Fatal("expected summary when catalog is over-budget")
	}
	if !strings.Contains(data.Summary, "run_skill") {
		t.Errorf("run_skill mode summary should point at run_skill, got %q", data.Summary)
	}

	fr2 := &fakeRenderer{rendered: "rendered"}
	_ = RenderAvailableSkills(fr2, skills, false)
	data2, ok := fr2.lastData.(availableSkillsData)
	if !ok {
		t.Fatalf("expected availableSkillsData, got %T", fr2.lastData)
	}
	if !strings.Contains(data2.Summary, "/skill:run:") {
		t.Errorf("inline-mode summary should point at /skill:run:<name>, got %q", data2.Summary)
	}
}

// TestTruncateSkillDescription verifies descriptions are capped at
// maxSkillDescriptionRunes runes including the ellipsis.
func TestTruncateSkillDescription(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{name: "short stays", in: "short", want: "short"},
		{name: "exact cap stays", in: strings.Repeat("a", 200), want: strings.Repeat("a", 200)},
		{name: "over cap truncated", in: strings.Repeat("a", 250), want: strings.Repeat("a", 199) + "…"},
		{name: "unicode", in: strings.Repeat("界", 300), want: strings.Repeat("界", 199) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateSkillDescription(tt.in)
			if got != tt.want {
				t.Errorf("truncateSkillDescription() = %q, want %q", got, tt.want)
			}
			if len([]rune(got)) > maxSkillDescriptionRunes {
				t.Errorf("result = %d runes, cap %d", len([]rune(got)), maxSkillDescriptionRunes)
			}
		})
	}
}

// extractSkillDescription pulls the description out of a rendered
// <skill …>DESC</skill> line.
func extractSkillDescription(t *testing.T, line string) string {
	t.Helper()
	start := strings.Index(line, ">")
	if start < 0 {
		t.Fatalf("no opening tag in %q", line)
	}
	end := strings.LastIndex(line, "</skill>")
	if end < 0 || end < start {
		t.Fatalf("no closing tag in %q", line)
	}
	return line[start+1 : end]
}
