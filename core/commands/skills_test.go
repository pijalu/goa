// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"strings"
	"testing"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/skills"
)

// testSkill helpers.
func testSkill(name, desc string, inline bool, command string) *skills.Skill {
	return &skills.Skill{
		Meta: skills.SkillMeta{
			Name:        name,
			Description: desc,
			Inline:      inline,
			Command:     command,
			InputSchema: nil,
		},
		Body: "Skill body for " + name,
	}
}

func TestListSkills_NoRegistry(t *testing.T) {
	w := newWriter()
	err := listSkills(w, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := w.Text()
	if !strings.Contains(text, "# Skills") {
		t.Errorf("expected skills header, got: %s", text)
	}
	if !strings.Contains(text, "refactor") {
		t.Errorf("expected fallback refactor, got: %s", text)
	}
}

func TestListSkills_WithRegistry(t *testing.T) {
	w := newWriter()
	reg := newSkillRegistry(map[string]*skills.Skill{
		"test-gen": testSkill("test-gen", "Generate unit tests", false, ""),
		"refactor": testSkill("refactor", "Refactor code", true, ""),
	})

	err := listSkills(w, reg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := w.Text()
	if !strings.Contains(text, "test-gen") {
		t.Errorf("expected test-gen, got: %s", text)
	}
	if !strings.Contains(text, "Refactor code") {
		t.Errorf("expected refactor desc, got: %s", text)
	}
	if !strings.Contains(text, "(inline)") {
		t.Errorf("expected (inline) marker, got: %s", text)
	}
	if !strings.Contains(text, "2 skill(s)") {
		t.Errorf("expected count, got: %s", text)
	}
}

func TestRunSkill_NoArgs(t *testing.T) {
	var buf strings.Builder
	err := runSkill(skillTestContext(&buf), nil, nil, nil)
	if err == nil {
		t.Fatal("expected error for no args")
	}
	if !strings.Contains(err.Error(), "usage:") {
		t.Errorf("expected usage error, got: %v", err)
	}
}

func TestRunSkill_NoRegistry(t *testing.T) {
	var buf strings.Builder
	err := runSkill(skillTestContext(&buf), nil, nil, []string{"test-gen"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Skill registry not available") {
		t.Errorf("expected registry-unavailable, got: %s", buf.String())
	}
}

func TestRunSkill_NotFound(t *testing.T) {
	var buf strings.Builder
	reg := newSkillRegistry(map[string]*skills.Skill{})

	err := runSkill(skillTestContext(&buf), reg, nil, []string{"nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Skill not found") {
		t.Errorf("expected not-found message, got: %s", buf.String())
	}
}

func TestRunSkill_Inline(t *testing.T) {
	var buf strings.Builder
	var submitted string
	submitFunc := func(s string) { submitted = s }
	reg := newSkillRegistry(map[string]*skills.Skill{
		"test-gen": testSkill("test-gen", "Generate tests", true, ""),
	})

	err := runSkill(skillTestContextWithHistory(&buf), reg, submitFunc, []string{"test-gen", "src/main.go"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if submitted == "" {
		t.Fatal("expected submitFunc to be called")
	}
	if !strings.Contains(submitted, "Skill: test-gen") {
		t.Errorf("expected skill header in submission, got: %s", submitted)
	}
	if !strings.Contains(submitted, "src/main.go") {
		t.Errorf("expected task in submission, got: %s", submitted)
	}
}

func TestRunSkill_InlineNoTask(t *testing.T) {
	var buf strings.Builder
	var submitted string
	submitFunc := func(s string) { submitted = s }
	reg := newSkillRegistry(map[string]*skills.Skill{
		"test-gen": testSkill("test-gen", "Generate tests", true, ""),
	})

	err := runSkill(skillTestContextWithHistory(&buf), reg, submitFunc, []string{"test-gen"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(submitted, "Apply the skill instructions") {
		t.Errorf("expected default task, got: %s", submitted)
	}
}

func TestRunSkill_InlineNoSubmitFunc(t *testing.T) {
	var buf strings.Builder
	reg := newSkillRegistry(map[string]*skills.Skill{
		"test-gen": testSkill("test-gen", "Generate tests", true, ""),
	})

	err := runSkill(skillTestContextWithHistory(&buf), reg, nil, []string{"test-gen", "src/main.go"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Skill: test-gen") {
		t.Errorf("expected skill body written to output, got: %s", buf.String())
	}
}

func TestRunSkill_Inline_BeforeConversation_LoadsIntoSystemPrompt(t *testing.T) {
	var buf strings.Builder
	var submitted string
	submitFunc := func(s string) { submitted = s }
	reg := newSkillRegistry(map[string]*skills.Skill{
		"telegram": testSkill("telegram", "Telegraphic style", true, ""),
	})

	ctx := skillTestContext(&buf)
	err := runSkill(ctx, reg, submitFunc, []string{"telegram"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if submitted != "" {
		t.Errorf("submitFunc should not be called before conversation starts, got: %s", submitted)
	}
	if !strings.Contains(buf.String(), "loaded into system prompt") {
		t.Errorf("expected load message, got: %s", buf.String())
	}
	current := ctx.AgentManager.CurrentMode()
	found := false
	for _, s := range current.Skills {
		if s == "telegram" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected 'telegram' in mode skills, got %v", current.Skills)
	}
}

func TestRunSkill_SubAgent(t *testing.T) {
	var buf strings.Builder
	reg := newSkillRegistry(map[string]*skills.Skill{
		"review": testSkill("review", "Code review", false, ""),
	})

	err := runSkill(skillTestContext(&buf), reg, nil, []string{"review", "src/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := buf.String()
	if !strings.Contains(text, "Running skill: review") {
		t.Errorf("expected running message, got: %s", text)
	}
	if !strings.Contains(text, "Sub-agent execution requires AgentPool") {
		t.Errorf("expected AgentPool message when no pool configured, got: %s", text)
	}
	if strings.Contains(text, "Skill body for review") {
		t.Errorf("skill body should NOT be shown when running, got: %s", text)
	}
}

func TestShowSkill_NoArgs(t *testing.T) {
	w := newWriter()
	err := showSkill(w, nil, nil)
	if err == nil {
		t.Fatal("expected error for no args")
	}
}

func TestShowSkill_WithRegistry(t *testing.T) {
	w := newWriter()
	reg := newSkillRegistry(map[string]*skills.Skill{
		"test-gen": testSkill("test-gen", "Generate tests", true, ""),
	})

	err := showSkill(w, reg, []string{"test-gen"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := w.Text()
	if !strings.Contains(text, "Skill: test-gen") {
		t.Errorf("expected skill name, got: %s", text)
	}
	if !strings.Contains(text, "Generate tests") {
		t.Errorf("expected description, got: %s", text)
	}
	if !strings.Contains(text, "inline") {
		t.Errorf("expected type, got: %s", text)
	}
	if !strings.Contains(text, "Skill body for test-gen") {
		t.Errorf("expected body, got: %s", text)
	}
}

func TestShowSkill_NotFound(t *testing.T) {
	w := newWriter()
	reg := newSkillRegistry(map[string]*skills.Skill{})

	err := showSkill(w, reg, []string{"nonexistent"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "Skill not found: nonexistent") {
		t.Errorf("expected not-found message, got: %s", w.Text())
	}
}

func TestShowSkill_NoRegistry(t *testing.T) {
	w := newWriter()
	err := showSkill(w, nil, []string{"test-gen"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(w.Text(), "details require SkillRegistry") {
		t.Errorf("expected registry-required message, got: %s", w.Text())
	}
}

func TestSkillInputSchemaDesc_Empty(t *testing.T) {
	result := skillInputSchemaDesc(nil)
	if result != "" {
		t.Errorf("expected empty for nil schema, got: %s", result)
	}

	result = skillInputSchemaDesc(map[string]any{})
	if result != "" {
		t.Errorf("expected empty for empty schema, got: %s", result)
	}
}

func TestSkillInputSchemaDesc_WithProps(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "File path",
			},
			"recursive": map[string]any{
				"type": "boolean",
			},
		},
	}

	result := skillInputSchemaDesc(schema)
	if !strings.Contains(result, "path (string): File path") {
		t.Errorf("expected path description, got: %s", result)
	}
	if !strings.Contains(result, "recursive (boolean)") {
		t.Errorf("expected recursive, got: %s", result)
	}
}

func TestSkillInputSchemaDesc_NoDesc(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"name": map[string]any{
				"type": "string",
			},
		},
	}

	result := skillInputSchemaDesc(schema)
	if !strings.Contains(result, "name (string)") {
		t.Errorf("expected name type only, got: %s", result)
	}
}

func TestSkillInputSchemaDesc_NoTypeOrDesc(t *testing.T) {
	schema := map[string]any{
		"properties": map[string]any{
			"raw": map[string]any{},
		},
	}

	result := skillInputSchemaDesc(schema)
	if !strings.Contains(result, "raw") {
		t.Errorf("expected raw param, got: %s", result)
	}
}

func TestUpdateCompletionsWithParams(t *testing.T) {
	reg := newSkillRegistry(map[string]*skills.Skill{
		"test-gen": testSkill("test-gen", "Generate tests", true, ""),
	})
	// Add input schema
	skill, _ := reg.Get("test-gen")
	skill.Meta.InputSchema = map[string]any{
		"properties": map[string]any{
			"path": map[string]any{
				"type":        "string",
				"description": "File path",
			},
		},
	}

	comps := []core.ArgCompletion{
		{Value: "run:test-gen", Description: "Generate tests"},
		{Value: "other", Description: "other command"},
	}

	result := UpdateCompletionsWithParams(comps, reg)
	if !strings.Contains(result[0].Description, "params:") {
		t.Errorf("expected params appended to run:test-gen, got: %s", result[0].Description)
	}
	if strings.Contains(result[1].Description, "params:") {
		t.Errorf("expected no params appended to other command, got: %s", result[1].Description)
	}
}

func TestSkillSubcommandCompletions_Empty(t *testing.T) {
	comps := skillSubcommandCompletions("")
	if len(comps) != 2 {
		t.Fatalf("expected 2 completions, got %d", len(comps))
	}
}

func TestSkillSubcommandCompletions_PrefixFilter(t *testing.T) {
	comps := skillSubcommandCompletions("sh")
	if len(comps) != 1 || comps[0].Value != "show" {
		t.Errorf("expected show only, got: %+v", comps)
	}
}

func TestSkillNameCompletions_NoRegistry(t *testing.T) {
	comps := skillNameCompletions("run", "", nil)
	if len(comps) != 0 {
		t.Errorf("expected empty, got: %+v", comps)
	}
}

func TestSkillNameCompletions_WithSkills(t *testing.T) {
	reg := newSkillRegistry(map[string]*skills.Skill{
		"test-gen": testSkill("test-gen", "Generate tests", true, ""),
		"refactor": testSkill("refactor", "Refactor code", false, ""),
	})

	comps := skillNameCompletions("run", "", reg)
	if len(comps) != 2 {
		t.Fatalf("expected 2 completions, got %d", len(comps))
	}
}

func TestSkillNameCompletions_SearchPrefix(t *testing.T) {
	reg := newSkillRegistry(map[string]*skills.Skill{
		"test-gen": testSkill("test-gen", "Generate tests", true, ""),
		"refactor": testSkill("refactor", "Refactor code", false, ""),
	})

	comps := skillNameCompletions("show", "test", reg)
	if len(comps) != 1 || comps[0].Value != "show:test-gen" {
		t.Errorf("expected show:test-gen only, got: %+v", comps)
	}
}
