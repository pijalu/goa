// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package commands

import (
	"fmt"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tools"
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
		Body:     "Skill body for " + name,
		FilePath: "/path/to/" + name + "/SKILL.md",
	}
}

func TestListSkills_NoRegistry(t *testing.T) {
	ctx := skillTestContext(new(strings.Builder))
	err := listSkills(ctx, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := ctx.OutputBuffer.String()
	if !strings.Contains(text, "# Skills") {
		t.Errorf("expected skills header, got: %s", text)
	}
	if !strings.Contains(text, "refactor") {
		t.Errorf("expected fallback refactor, got: %s", text)
	}
}

func TestListSkills_WithRegistry(t *testing.T) {
	ctx := skillTestContext(new(strings.Builder))
	reg := newSkillRegistry(map[string]*skills.Skill{
		"test-gen": testSkill("test-gen", "Generate unit tests", false, ""),
		"refactor": testSkill("refactor", "Refactor code", true, ""),
	})

	err := listSkills(ctx, reg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := ctx.OutputBuffer.String()
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
	if !strings.Contains(submitted, `skill "test-gen" is now active`) {
		t.Errorf("expected active-skill framing in submission, got: %s", submitted)
	}
	if !strings.Contains(submitted, "Skill body for test-gen") {
		t.Errorf("expected full skill body in submission, got: %s", submitted)
	}
	if !strings.Contains(submitted, "src/main.go") {
		t.Errorf("expected task in submission, got: %s", submitted)
	}
}

// TestRunSkill_InlineStripsNoise verifies inline injection strips SPDX license
// comment blocks and never emits the bare "[Skill:]" marker (bugs.md run_skill
// Issue B), while framing the body as instructions to execute (Issue A).
func TestRunSkill_InlineStripsNoise(t *testing.T) {
	var buf strings.Builder
	var submitted string
	submitFunc := func(s string) { submitted = s }
	sk := testSkill("commit-msg", "Generate commit message", true, "")
	sk.Body = "<!--\nSPDX-License-Identifier: GPL-3.0-or-later\n\nCopyright (C) 2026 Pierre Poissinger\n-->\n\n[Skill: commit-msg]\n# Skill: commit-msg\nGenerate the commit message from staged changes."
	reg := newSkillRegistry(map[string]*skills.Skill{"commit-msg": sk})

	err := runSkill(skillTestContextWithHistory(&buf), reg, submitFunc, []string{"commit-msg", "~/dev/frigolite"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if submitted == "" {
		t.Fatal("expected submitFunc to be called")
	}
	if strings.Contains(submitted, "SPDX-License-Identifier") {
		t.Errorf("SPDX header must be stripped from inline injection, got: %s", submitted)
	}
	if strings.Contains(submitted, "<!--") || strings.Contains(submitted, "-->") {
		t.Errorf("HTML comment must be stripped from inline injection, got: %s", submitted)
	}
	if strings.Contains(submitted, "[Skill: commit-msg]") {
		t.Errorf("[Skill:] marker must be stripped from inline injection, got: %s", submitted)
	}
	if !strings.Contains(submitted, "Generate the commit message from staged changes.") {
		t.Errorf("actionable body must be preserved, got: %s", submitted)
	}
	if !strings.Contains(submitted, "execute") {
		t.Errorf("expected execute-framing (Issue A), got: %s", submitted)
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
	if !strings.Contains(submitted, "Skill body for test-gen") {
		t.Errorf("expected full skill body in submission, got: %s", submitted)
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
	if !strings.Contains(buf.String(), "Skill body for test-gen") {
		t.Errorf("expected full skill body written to output, got: %s", buf.String())
	}
	if !strings.Contains(buf.String(), "src/main.go") {
		t.Errorf("expected task in output, got: %s", buf.String())
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

func TestRunSkill_Action_Inline(t *testing.T) {
	var buf strings.Builder
	reg := newSkillRegistry(map[string]*skills.Skill{
		"review": testSkill("review", "Code review", false, ""),
	})

	err := runSkill(skillTestContext(&buf), reg, nil, []string{"review", "src/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	text := buf.String()
	if strings.Contains(text, "Running skill 'review' in sub-agent") {
		t.Errorf("action skill should NOT use sub-agent, got: %s", text)
	}
	if strings.Contains(text, "Sub-agent execution is not available") {
		t.Errorf("action skill should NOT use sub-agent, got: %s", text)
	}
	if !strings.Contains(text, "Skill body for review") {
		t.Errorf("expected full skill body written to output, got: %s", text)
	}
	if !strings.Contains(text, "src/") {
		t.Errorf("expected task in output, got: %s", text)
	}
}

func TestRunSkill_ActionSkill_Inline_Execution(t *testing.T) {
	var buf strings.Builder
	var submitted string
	submitFunc := func(s string) { submitted = s }
	reg := newSkillRegistry(map[string]*skills.Skill{
		"golang-check": testSkill("golang-check", "Run static analysis checks", false, ""),
	})

	cfg := &config.Config{
		Skills: config.SkillsConfig{
			ExecutionMode: config.AgenticSkillModeInline,
		},
	}
	ctx := skillTestContextWithHistory(&buf)
	ctx.Config = cfg

	err := runSkill(ctx, reg, submitFunc, []string{"golang-check"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if submitted == "" {
		t.Fatal("expected submitFunc to be called with full skill body")
	}
	if !strings.Contains(submitted, `skill "golang-check" is now active`) {
		t.Errorf("expected active-skill framing in submission, got: %s", submitted)
	}
	if !strings.Contains(submitted, "Skill body for golang-check") {
		t.Errorf("expected full skill body in submission, got: %s", submitted)
	}
	if !strings.Contains(submitted, "Apply the skill instructions") {
		t.Errorf("expected apply instruction in submission, got: %s", submitted)
	}
	if strings.Contains(buf.String(), "Running skill: golang-check") {
		t.Errorf("action skill should NOT run as sub-agent, got: %s", buf.String())
	}
}

func TestRunSkill_ActionSkill_BeforeConversation_SubmitsAsUserMessage(t *testing.T) {
	var buf strings.Builder
	var submitted string
	submitFunc := func(s string) { submitted = s }
	reg := newSkillRegistry(map[string]*skills.Skill{
		"golang-check": testSkill("golang-check", "Run static analysis checks", false, ""),
	})

	ctx := skillTestContext(&buf)
	err := runSkill(ctx, reg, submitFunc, []string{"golang-check", "src/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if submitted == "" {
		t.Fatal("expected action skill to be submitted as user message before conversation")
	}
	if !strings.Contains(submitted, `skill "golang-check" is now active`) {
		t.Errorf("expected active-skill framing in submission, got: %s", submitted)
	}
	if !strings.Contains(submitted, "Skill body for golang-check") {
		t.Errorf("expected full skill body in submission, got: %s", submitted)
	}
	if !strings.Contains(submitted, "src/") {
		t.Errorf("expected task in submission, got: %s", submitted)
	}
	if strings.Contains(buf.String(), "loaded into system prompt") {
		t.Errorf("action skill should NOT be loaded into system prompt, got: %s", buf.String())
	}
	current := ctx.AgentManager.CurrentMode()
	for _, s := range current.Skills {
		if s == "golang-check" {
			t.Errorf("action skill should NOT be added to mode skills, got %v", current.Skills)
		}
	}
}

func TestRunSkill_ActionSkill_SubAgent_Execution(t *testing.T) {
	var buf strings.Builder
	var submitted string
	submitFunc := func(s string) { submitted = s }
	reg := newSkillRegistry(map[string]*skills.Skill{
		"golang-check": testSkill("golang-check", "Run static analysis checks", false, ""),
	})
	runner := &fakeSkillSubAgentRunner{result: "Found 2 issues."}
	cfg := &config.Config{
		Skills: config.SkillsConfig{
			ExecutionMode: config.AgenticSkillModeSubAgent,
		},
	}
	ctx := skillTestContextWithHistory(&buf)
	ctx.Config = cfg
	ctx.SkillSubAgentRunner = runner

	err := runSkill(ctx, reg, submitFunc, []string{"golang-check", "src/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(runner.calls) != 1 {
		t.Fatalf("expected 1 sub-agent run, got %d", len(runner.calls))
	}
	if runner.calls[0].systemPrompt != "You are a skill executor. Execute the instructions in the user message and return the final output. Do not plan, summarize, or explain the instructions; perform the work immediately. Use the bash tool for shell commands. Return only the final output." {
		t.Errorf("expected executor system prompt, got: %s", runner.calls[0].systemPrompt)
	}
	wantTask := "[Skill: golang-check]\nSkill body for golang-check\n\nTask: src/\n"
	if runner.calls[0].task != wantTask {
		t.Errorf("expected task %q, got: %s", wantTask, runner.calls[0].task)
	}
	if runner.calls[0].allowedTools != nil {
		t.Errorf("expected nil allowedTools for skill with no tools, got: %v", runner.calls[0].allowedTools)
	}
	if submitted == "" {
		t.Fatal("expected submitFunc to be called with sub-agent result")
	}
	if !strings.Contains(submitted, "[Skill result: golang-check]") {
		t.Errorf("expected skill result header in submission, got: %s", submitted)
	}
	if !strings.Contains(submitted, "Found 2 issues.") {
		t.Errorf("expected sub-agent result in submission, got: %s", submitted)
	}
	if !strings.Contains(buf.String(), "Running skill 'golang-check' in sub-agent") {
		t.Errorf("expected sub-agent running message, got: %s", buf.String())
	}
}

func TestRunSkill_SubAgent_NoRunner_Warns(t *testing.T) {
	var buf strings.Builder
	reg := newSkillRegistry(map[string]*skills.Skill{
		"golang-check": testSkill("golang-check", "Run static analysis checks", false, ""),
	})
	cfg := &config.Config{
		Skills: config.SkillsConfig{
			ExecutionMode: config.AgenticSkillModeSubAgent,
		},
	}
	ctx := skillTestContextWithHistory(&buf)
	ctx.Config = cfg

	err := runSkill(ctx, reg, nil, []string{"golang-check"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Sub-agent execution is not available") {
		t.Errorf("expected missing runner warning, got: %s", buf.String())
	}
}

func TestRunSkill_SubAgent_Error_Warns(t *testing.T) {
	var buf strings.Builder
	reg := newSkillRegistry(map[string]*skills.Skill{
		"golang-check": testSkill("golang-check", "Run static analysis checks", false, ""),
	})
	runner := &fakeSkillSubAgentRunner{err: fmt.Errorf("pool not ready")}
	cfg := &config.Config{
		Skills: config.SkillsConfig{
			ExecutionMode: config.AgenticSkillModeSubAgent,
		},
	}
	ctx := skillTestContextWithHistory(&buf)
	ctx.Config = cfg
	ctx.SkillSubAgentRunner = runner

	err := runSkill(ctx, reg, nil, []string{"golang-check"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !strings.Contains(buf.String(), "Skill 'golang-check' failed: pool not ready") {
		t.Errorf("expected error message, got: %s", buf.String())
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

func TestRunSkill_SubAgent_ViaRunSkillTool(t *testing.T) {
	var buf strings.Builder
	var submitted string
	submitFunc := func(s string) { submitted = s }
	reg := newSkillRegistry(map[string]*skills.Skill{
		"golang-check": testSkill("golang-check", "Run static analysis checks", false, ""),
	})
	cfg := &config.Config{
		Skills: config.SkillsConfig{
			ExecutionMode: config.AgenticSkillModeSubAgent,
		},
	}
	ctx := skillTestContextWithHistory(&buf)
	ctx.Config = cfg
	tool := &fakeTool{
		name:   "run_skill",
		result: "Found 2 issues.",
	}
	tr := tools.NewToolRegistry()
	tr.Register(tool)
	ctx.ToolRegistry = tr

	err := runSkill(ctx, reg, submitFunc, []string{"golang-check", "src/"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tool.input == "" {
		t.Fatal("expected run_skill tool to be called")
	}
	if submitted == "" {
		t.Fatal("expected result to be submitted")
	}
	if !strings.Contains(submitted, "Found 2 issues.") {
		t.Errorf("expected tool result in submission, got: %s", submitted)
	}
	if !strings.Contains(buf.String(), "via run_skill tool") {
		t.Errorf("expected tool invocation message, got: %s", buf.String())
	}
}

type fakeTool struct {
	name   string
	result string
	err    error
	input  string
}

func (f *fakeTool) Schema() agentic.ToolSchema {
	return agentic.ToolSchema{Name: f.name}
}

func (f *fakeTool) Execute(input string) (string, error) {
	f.input = input
	return f.result, f.err
}

func (f *fakeTool) IsRetryable(err error) bool { return false }
