// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/memory"
	"github.com/pijalu/goa/prompts"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tools"
)

func newTestSubsystems(dir string) *subsystems {
	cfg := &config.Config{ConfigDir: dir}
	promptReg := prompts.NewRegistry(prompts.EmbeddedFS(), filepath.Join(dir, ".goa", "prompts"), filepath.Join(dir, ".goa", "prompts"))
	modeReg := core.NewModeRegistry(promptReg)
	return &subsystems{
		cfg:           cfg,
		modeRegistry:  modeReg,
		skillRegistry: skills.NewSkillRegistry(nil),
		promptReg:     promptReg,
	}
}

func TestBuildSystemPrompt_IncludesMemorySummaries(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".goa", "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatalf("create memory dir: %v", err)
	}
	content := "---\nsummary: Project uses Go 1.25 and a component-based TUI.\n---\n\n# Full memory\n\nLots of details here."
	if err := os.WriteFile(filepath.Join(memDir, "project.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	subs := newTestSubsystems(dir)
	subs.memStore = memory.NewMemoryStore(dir, dir)
	subs.MemoryEnabled = true
	subs.MemoryBudget = 1024

	got := buildSystemPrompt(subs)
	if !strings.Contains(got, "<memory>") {
		t.Errorf("missing <memory> section:\n%s", got)
	}
	if !strings.Contains(got, "Project uses Go 1.25") {
		t.Errorf("missing memory summary:\n%s", got)
	}
	if strings.Contains(got, "Lots of details here") {
		t.Errorf("full memory content should not be injected:\n%s", got)
	}
}

func TestBuildSystemPrompt_OmitsMemoryWhenDisabled(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".goa", "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatalf("create memory dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "project.md"), []byte("---\nsummary: Should not appear.\n---\n"), 0644); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	subs := newTestSubsystems(dir)
	subs.memStore = memory.NewMemoryStore(dir, dir)
	subs.MemoryEnabled = false
	subs.MemoryBudget = 1024

	got := buildSystemPrompt(subs)
	if strings.Contains(got, "<memory>") {
		t.Errorf("<memory> should be omitted when disabled:\n%s", got)
	}
}

func TestBuildSystemPrompt_SkipsMemoryWithoutSummary(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".goa", "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatalf("create memory dir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(memDir, "legacy.md"), []byte("No summary here."), 0644); err != nil {
		t.Fatalf("write memory: %v", err)
	}

	subs := newTestSubsystems(dir)
	subs.memStore = memory.NewMemoryStore(dir, dir)
	subs.MemoryEnabled = true
	subs.MemoryBudget = 1024

	got := buildSystemPrompt(subs)
	if strings.Contains(got, "No summary here") {
		t.Errorf("memory without summary should not be injected:\n%s", got)
	}
}

func TestExtractMemorySummary(t *testing.T) {
	cases := []struct {
		name    string
		content string
		want    string
	}{
		{
			name:    "frontmatter",
			content: "---\nsummary: Short summary\n---\n\nFull content.",
			want:    "Short summary",
		},
		{
			name:    "section",
			content: "# Memory\n\n## Summary\nSection summary\n\n## Details\nMore text.",
			want:    "Section summary",
		},
		{
			name:    "none",
			content: "Just full content without summary.",
			want:    "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := extractMemorySummary(tc.content)
			if got != tc.want {
				t.Errorf("extractMemorySummary() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestBuildMemorySectionRespectsBudget(t *testing.T) {
	dir := t.TempDir()
	memDir := filepath.Join(dir, ".goa", "memory")
	if err := os.MkdirAll(memDir, 0755); err != nil {
		t.Fatalf("create memory dir: %v", err)
	}
	// Two memories with 50-token summaries each.
	for _, name := range []string{"a.md", "b.md"} {
		content := "---\nsummary: " + strings.Repeat("word ", 50) + "\n---\n"
		if err := os.WriteFile(filepath.Join(memDir, name), []byte(content), 0644); err != nil {
			t.Fatalf("write memory: %v", err)
		}
	}

	subs := newTestSubsystems(dir)
	subs.memStore = memory.NewMemoryStore(dir, dir)
	subs.MemoryEnabled = true
	subs.MemoryBudget = 80 // Fits one summary but not both.

	got := buildSystemPrompt(subs)
	// Should include the first memory but not both.
	if !strings.Contains(got, "<memory>") {
		t.Errorf("missing <memory> section:\n%s", got)
	}
	count := strings.Count(got, "File: ")
	if count != 1 {
		t.Errorf("expected 1 memory entry, got %d", count)
	}
}

func TestBuildSystemPrompt_IncludesActiveSkillBodies(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".goa", "skills")
	skillRoot := filepath.Join(skillsDir, "telegram")
	if err := os.MkdirAll(skillRoot, 0755); err != nil {
		t.Fatalf("create skill dir: %v", err)
	}
	skillContent := "---\nname: telegram\ninline: true\n---\nUse telegraphic style for all thinking and reasoning."
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(skillContent), 0644); err != nil {
		t.Fatalf("write skill: %v", err)
	}

	cfg := &config.Config{ConfigDir: dir}
	ss := core.NewSessionState(internal.ModeState{
		Major:    internal.MajorCoder,
		Autonomy: internal.AutonomyYolo,
		Skills:   []string{"telegram"},
	})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	agentMgr := core.NewAgentManager(cfg, nil, nil, ss, tuiEvents, "")

	skillReg := skills.NewSkillRegistry([]string{skillsDir})
	if err := skillReg.LoadAll(); err != nil {
		t.Fatalf("load skills: %v", err)
	}

	subs := newTestSubsystems(dir)
	subs.agentMgr = agentMgr
	subs.skillRegistry = skillReg

	got := buildSystemPrompt(subs)
	if !strings.Contains(got, "<skills>") {
		t.Errorf("missing <skills> section:\n%s", got)
	}
	if !strings.Contains(got, "Use telegraphic style") {
		t.Errorf("missing active skill body:\n%s", got)
	}
	if !strings.Contains(got, `<skill name="telegram">`) {
		t.Errorf("missing skill name tag:\n%s", got)
	}
}

func TestBuildActiveSkillsSection_NoSkills(t *testing.T) {
	subs := newTestSubsystems(t.TempDir())
	if got := buildSystemPrompt(subs); strings.Contains(got, "<skills>") {
		t.Errorf("<skills> should be omitted when no skills are active:\n%s", got)
	}
}

func TestFilterToolsForCurrentMode_AppliesAllowedTools(t *testing.T) {
	promptReg := prompts.NewRegistry(prompts.EmbeddedFS())
	modeReg := core.NewModeRegistry(promptReg)

	// Register a custom mode with AllowedTools restriction.
	modeReg.RegisterMajor(core.MajorModeSpec{
		Major:        "restricted",
		Name:         "Restricted Mode",
		AllowedTools: []string{"read", "search"},
	})

	cfg := &config.Config{}
	ss := core.NewSessionState(internal.ModeState{Major: "restricted", Autonomy: internal.AutonomySolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := core.NewAgentManager(cfg, nil, nil, ss, tuiEvents, "")

	toolReg := tools.NewToolRegistry()
	// Register a few tools including read/search.
	toolReg.Register(&tools.ReadFileTool{})
	toolReg.Register(&tools.SearchTool{})
	toolReg.Register(&tools.BashTool{})

	subs := &subsystems{
		cfg:          cfg,
		modeRegistry: modeReg,
		agentMgr:     am,
	}

	filtered := filterToolsForCurrentMode(subs, toolReg.All())
	got := make([]string, 0, len(filtered))
	for _, t := range filtered {
		got = append(got, t.Schema().Name)
	}

	// Should only contain read and search.
	if len(got) != 2 || got[0] != "read" || got[1] != "search" {
		t.Errorf("filtered tools = %v, want [read search]", got)
	}
}

func TestFilterToolsForCurrentMode_EmptyAllowed_ReturnsAll(t *testing.T) {
	promptReg := prompts.NewRegistry(prompts.EmbeddedFS())
	modeReg := core.NewModeRegistry(promptReg)

	cfg := &config.Config{}
	ss := core.NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomySolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	am := core.NewAgentManager(cfg, nil, nil, ss, tuiEvents, "")

	toolReg := tools.NewToolRegistry()
	toolReg.Register(&tools.ReadFileTool{})
	toolReg.Register(&tools.BashTool{})

	subs := &subsystems{
		cfg:          cfg,
		modeRegistry: modeReg,
		agentMgr:     am,
	}

	filtered := filterToolsForCurrentMode(subs, toolReg.All())
	if len(filtered) != 2 {
		t.Errorf("expected 2 tools (no filter), got %d", len(filtered))
	}
}

func TestBuildSystemPrompt_IncludesSelfDocSection(t *testing.T) {
	subs := newTestSubsystems(t.TempDir())
	got := buildSystemPrompt(subs)
	if !strings.Contains(got, "<goa_documentation>") {
		t.Errorf("missing <goa_documentation> section:\n%s", got)
	}
	if !strings.Contains(got, "goa://docs/SKILLS.md") {
		t.Errorf("missing goa://docs/SKILLS.md reference:\n%s", got)
	}
	if !strings.Contains(got, "goa://docs/TOOLS.md") {
		t.Errorf("missing goa://docs/TOOLS.md reference:\n%s", got)
	}
	if !strings.Contains(got, "To create or use user skills") {
		t.Errorf("missing skills guidance:\n%s", got)
	}
}

func TestBuildSelfDocSection_GeneratedFromEmbeddedDocs(t *testing.T) {
	section := buildSelfDocSection()
	if section == "" {
		t.Fatal("expected non-empty self-doc section")
	}
	if !strings.Contains(section, "<goa_documentation>") {
		t.Errorf("missing <goa_documentation> tag:\n%s", section)
	}
	if !strings.Contains(section, "goa://docs/ARCHITECTURE.md") {
		t.Errorf("missing ARCHITECTURE reference:\n%s", section)
	}
	if !strings.Contains(section, "To create or use user skills: read goa://docs/SKILLS.md") {
		t.Errorf("missing explicit skills guidance:\n%s", section)
	}
	if !strings.Contains(section, "To build or load plugins: read goa://docs/PLUGINS.md") {
		t.Errorf("missing explicit plugins guidance:\n%s", section)
	}
	if !strings.Contains(section, "To configure Goa") {
		t.Errorf("missing explicit configuration guidance:\n%s", section)
	}
	if !strings.Contains(section, "To call tools and understand tool schemas") {
		t.Errorf("missing explicit tools guidance:\n%s", section)
	}
}
