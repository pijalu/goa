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
	"github.com/pijalu/goa/provider"
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
	// Memory store strips .md and sorts by mtime descending.
	// Exactly one of "a: " or "b: " should be present.
	hasA := strings.Contains(got, "a: ")
	hasB := strings.Contains(got, "b: ")
	if !hasA && !hasB {
		t.Errorf("expected one memory entry")
	}
	if hasA && hasB {
		t.Errorf("expected only 1 memory entry (budget 80), got both")
	}
}

func TestBuildSystemPrompt_IncludesActiveSkillListings(t *testing.T) {
	dir := t.TempDir()
	skillsDir := filepath.Join(dir, ".goa", "skills")
	skillRoot := filepath.Join(skillsDir, "telegram")
	if err := os.MkdirAll(skillRoot, 0755); err != nil {
		t.Fatalf("create skill dir: %v", err)
	}
	skillContent := "---\nname: telegram\ndescription: Use telegraphic style for all thinking and reasoning.\ninline: true\n---\nUse telegraphic style for all thinking and reasoning."
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
	if !strings.Contains(got, "<active_skills>") {
		t.Errorf("missing <active_skills> section:\n%s", got)
	}
	if !strings.Contains(got, "telegram") {
		t.Errorf("missing active skill name:\n%s", got)
	}
	if strings.Contains(got, "<skill name=\"telegram\">") {
		t.Errorf("active skill should not be inlined as a full body:\n%s", got)
	}
}

func TestBuildActiveSkillsSection_NoSkills(t *testing.T) {
	subs := newTestSubsystems(t.TempDir())
	if got := buildSystemPrompt(subs); strings.Contains(got, "<active_skills>") {
		t.Errorf("<active_skills> should be omitted when no skills are active:\n%s", got)
	}
}

// writeTestSkill creates a SKILL.md under dir/.goa/skills/<name>/.
func writeTestSkill(t *testing.T, dir, name, frontmatter string) {
	t.Helper()
	skillRoot := filepath.Join(dir, ".goa", "skills", name)
	if err := os.MkdirAll(skillRoot, 0755); err != nil {
		t.Fatalf("create skill dir: %v", err)
	}
	content := "---\n" + frontmatter + "\n---\nbody"
	if err := os.WriteFile(filepath.Join(skillRoot, "SKILL.md"), []byte(content), 0644); err != nil {
		t.Fatalf("write skill: %v", err)
	}
}

// In inline execution mode the run_skill tool is registered (it returns the
// skill body as its tool result), so the <available_skills> listing must
// advertise action skills with tool="run_skill".
func TestAvailableSkillsSection_InlineModeRunSkill(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "review", "name: review\ndescription: Review code\ncategory: action")

	skillReg := skills.NewSkillRegistry([]string{filepath.Join(dir, ".goa", "skills")})
	if err := skillReg.LoadAll(); err != nil {
		t.Fatalf("load skills: %v", err)
	}

	subs := newTestSubsystems(dir)
	subs.skillRegistry = skillReg
	// inline execution mode: run_skill IS registered (inline variant)
	subs.toolRegistry = tools.NewToolRegistry()
	subs.toolRegistry.Register(&mockTool{name: "run_skill"})

	got := availableSkillsSection(subs)
	if !strings.Contains(got, `tool="run_skill"`) {
		t.Errorf("inline mode should advertise run_skill:\n%s", got)
	}
	if strings.Contains(got, "/skill:run:") {
		t.Errorf("action skills must not be advertised with the user-only slash command:\n%s", got)
	}
}

// In sub-agent execution mode the run_skill tool is registered, so action
// skills are advertised with tool="run_skill".
func TestAvailableSkillsSection_SubAgentModeRunSkill(t *testing.T) {
	dir := t.TempDir()
	writeTestSkill(t, dir, "review", "name: review\ndescription: Review code\ncategory: action")

	skillReg := skills.NewSkillRegistry([]string{filepath.Join(dir, ".goa", "skills")})
	if err := skillReg.LoadAll(); err != nil {
		t.Fatalf("load skills: %v", err)
	}

	subs := newTestSubsystems(dir)
	subs.skillRegistry = skillReg
	subs.cfg.Skills.ExecutionMode = config.AgenticSkillModeSubAgent
	subs.toolRegistry = tools.NewToolRegistry()
	subs.toolRegistry.Register(&mockTool{name: "run_skill"})

	got := availableSkillsSection(subs)
	if !strings.Contains(got, `tool="run_skill"`) {
		t.Errorf("sub-agent mode should advertise run_skill:\n%s", got)
	}
}

func TestBuildSystemPrompt_SmallContextBudget(t *testing.T) {
	dir := t.TempDir()
	cfg := &config.Config{ConfigDir: dir}
	ss := core.NewSessionState(internal.ModeState{Major: internal.MajorCoder, Autonomy: internal.AutonomySolo})
	tuiEvents := event.MakeBus(10, 10, 10, 10)
	agentMgr := core.NewAgentManager(cfg, nil, nil, ss, tuiEvents, "")

	subs := newTestSubsystems(dir)
	subs.cfg = cfg
	subs.agentMgr = agentMgr
	subs.contextFiles = []internal.ContextFile{
		{Path: "AGENTS.md", Content: strings.Repeat("project rule line\n", 200)},
	}
	subs.ContextWindow = 8192

	got := buildSystemPrompt(subs)
	budget := systemPromptBudget(8192)
	if len(got) > budget {
		t.Errorf("system prompt length %d exceeds budget %d", len(got), budget)
	}
	// Mode and project context should remain.
	if !strings.Contains(got, "<project_context>") {
		t.Errorf("<project_context> should be kept for 8k context:\n%s", got)
	}
	if !strings.Contains(got, "coder agent") {
		t.Errorf("mode prompt should be kept for 8k context:\n%s", got)
	}
	// Low-priority sections are dropped only when the budget is exhausted.
	// The assertions above verify the budget is enforced; this test does not
	// mandate which specific section is dropped because the budget is large
	// enough to keep the compact sections in this minimal test.
}

func TestModelContextWindow_LocalProviderFallback(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "lmstudio",
		ActiveModel:    "test",
		Providers: []config.ProviderConfig{
			{ID: "lmstudio", Endpoint: "http://localhost:1234/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "test", Provider: "lmstudio", Model: "test-model"},
		},
	}
	pm := provider.NewProviderManager(cfg)
	subs := newTestSubsystems(t.TempDir())
	subs.providerMgr = pm

	got := modelContextWindow(subs)
	if got != 8192 {
		t.Errorf("modelContextWindow for local provider = %d, want 8192 fallback", got)
	}
}

func TestModelContextWindow_ResolvedContextWindow(t *testing.T) {
	cfg := &config.Config{
		ActiveProvider: "lmstudio",
		ActiveModel:    "test",
		Providers: []config.ProviderConfig{
			{ID: "lmstudio", Endpoint: "http://localhost:1234/v1"},
		},
		Models: []config.ModelConfig{
			{ID: "test", Provider: "lmstudio", Model: "test-model", ContextWindow: 32768},
		},
	}
	pm := provider.NewProviderManager(cfg)
	subs := newTestSubsystems(t.TempDir())
	subs.providerMgr = pm

	got := modelContextWindow(subs)
	if got != 32768 {
		t.Errorf("modelContextWindow = %d, want 32768", got)
	}
}

func TestModelContextWindow_Override(t *testing.T) {
	subs := newTestSubsystems(t.TempDir())
	subs.ContextWindow = 4096

	got := modelContextWindow(subs)
	if got != 4096 {
		t.Errorf("modelContextWindow = %d, want 4096 override", got)
	}
}

func TestBuildSystemPrompt_ContextWindowOverride(t *testing.T) {
	subs := newTestSubsystems(t.TempDir())
	// Without an override and no provider, budget is unlimited.
	got := buildSystemPrompt(subs)
	if len(got) == 0 {
		t.Errorf("expected non-empty prompt when context window is unknown")
	}

	// With a small override, the budget should be enforced.
	subs.ContextWindow = 8192
	got = buildSystemPrompt(subs)
	if len(got) > systemPromptBudget(8192) {
		t.Errorf("prompt length %d exceeds budget %d with override", len(got), systemPromptBudget(8192))
	}
}

func TestSystemPromptBudget_ConservativeAtAllSizes(t *testing.T) {
	cases := []struct {
		ctxWindow int
		maxWant   int
	}{
		{8192, 6000},
		{16384, 9000},
		{32768, 14000},
		{65536, 20000},
		{128000, 30000},
		{256000, 30000},
	}
	for _, tc := range cases {
		got := systemPromptBudget(tc.ctxWindow)
		if got > tc.maxWant {
			t.Errorf("systemPromptBudget(%d) = %d, want <= %d", tc.ctxWindow, got, tc.maxWant)
		}
	}
}

func TestApplySystemPromptBudget_DropsLowPrioritySections(t *testing.T) {
	parts := []string{
		"mode prompt",        // highest priority
		"project context",    // next
		"memory",             // next
		"active skills",      // next
		"available skills",   // next
		"self documentation", // lowest priority
	}
	budget := len(strings.Join(parts[:4], "\n\n")) + 5

	got := applySystemPromptBudget(parts, budget)
	if strings.Contains(got, "self documentation") {
		t.Errorf("lowest-priority section should be dropped:\n%s", got)
	}
	if !strings.Contains(got, "mode prompt") {
		t.Errorf("highest-priority section should be kept:\n%s", got)
	}
}

func TestApplySystemPromptBudget_TruncatesHighestPriority(t *testing.T) {
	parts := []string{"this is a very long mode prompt that exceeds the entire budget"}
	budget := 20

	got := applySystemPromptBudget(parts, budget)
	if len(got) > budget+10 {
		t.Errorf("highest-priority section should be truncated to fit budget, got length %d", len(got))
	}
	if !strings.Contains(got, "…") {
		t.Errorf("expected truncation marker:\n%s", got)
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
	if !strings.Contains(got, "SKILLS") {
		t.Errorf("missing SKILLS reference:\n%s", got)
	}
	if !strings.Contains(got, "TOOLS") {
		t.Errorf("missing TOOLS reference:\n%s", got)
	}
	if !strings.Contains(got, "Read embedded docs") {
		t.Errorf("missing read-tool guidance:\n%s", got)
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
	if !strings.Contains(section, "ARCHITECTURE") {
		t.Errorf("missing ARCHITECTURE reference:\n%s", section)
	}
	if !strings.Contains(section, "Read embedded docs") {
		t.Errorf("missing read-tool guidance:\n%s", section)
	}
}
