// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"strings"
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tools/todo"
)

// Startup context accounting (bugs.md B52): these tests measure and pin the
// fixed first-request payload (system prompt + eager tool schemas).

// newStartupContextSubsystems builds a subsystems snapshot representative of
// a default headless start in a scratch project: default config cascade,
// the runtime tool registration path, the goal tool (default-on), the skill
// runner, and a couple of AGENTS.md context files typical of a project.
// MCP and plugins stay unconnected (per-project, unguarded variance).
func newStartupContextSubsystems(t *testing.T) *subsystems {
	t.Helper()
	dir := t.TempDir()

	// Representative project context: a root AGENTS.md of typical size.
	writeFile(t, dir, "AGENTS.md", strings.Repeat(strings.Repeat("x", 76)+"\n", 16)) // ~1.2KB project guide
	subs := newTestSubsystems(dir)

	cfg, err := config.NewCascadeLoader(dir, "", nil).Load()
	if err != nil {
		t.Fatalf("load default config: %v", err)
	}
	subs.cfg = cfg

	// Runtime tool registration (registerTools) + the always-on extras the
	// app registers outside registerTools (subsystems.go): goal, webfetch
	// (default-on), lsp (default-on), and the run_skill runner. todo_list
	// ships OFF by default.
	reg := tools.NewToolRegistry()
	registerTools(reg, nil, nil, dir, cfg, nil, false)
	subs.toolRegistry = reg
	subs.contextFiles = internal.LoadProjectContextFiles(dir, cfg.ConfigDir)

	gm := core.NewGoalManager(dir)
	registerGoalTools(reg, gm, func() bool { return cfg.Tools.Enabled.Goal }, nil, nil, nil)
	if cfg.Tools.Enabled.Todo {
		reg.Register(&todo.TodoListTool{Mode: gm.Mode})
	}
	registerWebFetchTool(reg, core.NewSessionStore(dir+"/.goa"), cfg, dir)
	subs.skillRegistry = newStartupSkillRegistry(t, cfg, dir)
	registerSkillRunner(reg, subs.skillRegistry, nil, subs.promptReg, cfg)
	return subs
}

// newStartupSkillRegistry mirrors newSkillRegistry (subsystems_skills.go) for
// a scratch project: default dirs + embedded shipped skills with the default
// gate (embedded off except the shipped sticky default, e.g. telegram).
func newStartupSkillRegistry(t *testing.T, cfg *config.Config, dir string) *skills.SkillRegistry {
	t.Helper()
	skillDirs := append(config.DefaultSkillDirs(dir), cfg.Skills.Dirs...)
	reg := skills.NewSkillRegistry(skillDirs)
	reg.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	reg.SetDisabled(cfg.Skills.Disabled)
	reg.SetEmbeddedDefaultDisabled(skills.DefaultEmbeddedOffNames(skills.EmbeddedSkillsFS))
	allow, embeddedScoped := cfg.Skills.SkillGateLists()
	reg.SetEnabled(allow)
	reg.SetEmbeddedEnabled(embeddedScoped)
	reg.SetStickyOverrides(cfg.Skills.Sticky, cfg.Skills.StickyOff)
	if err := reg.LoadAll(); err != nil {
		t.Fatalf("load skills: %v", err)
	}
	return reg
}

func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(dir+"/"+name, []byte(content), 0644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// TestStartupContextBreakdown prints the per-component byte/token table for
// the default first request. Run with:
//
//	go test -run TestStartupContextBreakdown -v ./internal/app
//
// The table is the repeatable B52 measurement: system prompt sections
// (post-budget, i.e. what is actually sent) and every eager tool schema;
// deferred tools are withheld and ship only through the tool_search catalog.
func TestStartupContextBreakdown(t *testing.T) {
	subs := newStartupContextSubsystems(t)
	if got := buildSystemPrompt(subs); !strings.Contains(got, "<project_context>") {
		t.Fatalf("project context missing from prompt:\n%s", got)
	}
	t.Log("\n" + StartupContextBreakdown(subs).Report())
}

// Startup context budget (bugs.md B52, 2026-08-26): pin the total fixed
// first-request payload. Measured baseline history:
//   - 31,351 bytes pre-B52: DeferralThreshold (8) kept deferral dormant on the
//     B46 default tool set, so mcp/webfetch/run_skill/todo_list schemas all
//     shipped eagerly.
//   - 24,865 bytes post-B52: threshold lowered to 2, deferral active —
//     −6,486 bytes (~1,620 tokens) per request, every request.
// Ceiling = post-B52 baseline + ~4.5% headroom. If this fails, shrink a
// component first (see TestStartupContextBreakdown -v for the per-component
// table); raise the ceiling only with a fresh measured breakdown as
// justification.
func TestStartupContextBudget(t *testing.T) {
	subs := newStartupContextSubsystems(t)
	cs := StartupContextBreakdown(subs)
	total := cs.totalBytes()

	const maxStartupContextBytes = 26000
	if total > maxStartupContextBytes {
		t.Errorf("startup context = %d bytes, ceiling %d — the fixed payload ships with EVERY provider request; "+
			"shrink a component (see TestStartupContextBreakdown -v) or raise the ceiling with a measured justification",
			total, maxStartupContextBytes)
	}
	t.Logf("startup context total: %d bytes (~%d tokens), ceiling %d", total, TokensFromBytes(total), maxStartupContextBytes)
}
