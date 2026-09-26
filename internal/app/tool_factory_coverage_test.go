// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"testing"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	internal "github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tools/ask"
)

// runtimeFactorySubsystems builds the host state makeToolFactory needs to
// construct every re-enablable tool. The resources mirror what a real session
// has up (live registry, goal manager, PTY manager, command router for the goa
// tool, sub-agent pool, event bus, session store and the interactive clarify
// host callback), so a name that still cannot be built here is a genuine
// product gap rather than a fixture artifact.
func runtimeFactorySubsystems(t *testing.T, cfg *config.Config) *subsystems {
	t.Helper()
	dir := t.TempDir()
	registry := core.NewCommandRegistry()
	router := core.NewCommandRouter(registry, core.NewDocEngine(registry))
	skillsRegistry := skills.NewSkillRegistry(nil)
	skillsRegistry.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	return &subsystems{
		cfg:           cfg,
		projectDir:    dir,
		toolRegistry:  tools.NewToolRegistry(),
		skillRegistry: skillsRegistry,
		events:        event.MakeBus(16, 16, 16, 16),
		goalManager:   core.NewGoalManager(dir),
		sessionStore:  core.NewSessionStore(dir),
		ptyMgr:        internal.NewPTYManager(),
		goaTool:       core.NewGoaCommandToolWithContextFn(router, func() core.Context { return core.Context{} }),
		agentPool:     multiagent.NewAgentPool(provider.Model{}, provider.StreamOptions{}, nil),
		clarifyFn: func(title, summary, question string, options []string, step, total int) (string, bool) {
			return "answered", true
		},
	}
}

// factoryConfig returns a config like the cascade-loaded one a real session
// runs with: the webfetch feature switch on, python/run_code available, and the
// tools.enabled.lsp flag true — the /config toggle flips that very flag BEFORE
// the factory builds the tool, so the post-toggle state is what the factory
// sees (LSP's manager gate reads the same flag).
func factoryConfig() *config.Config {
	cfg := &config.Config{}
	cfg.Tools.WebFetch.Enabled = true
	cfg.Tools.Enabled.SetEnabled("goal", true)
	cfg.Tools.Enabled.LSP = true
	return cfg
}

// restartOnlyTools are the configurable tools the factory deliberately cannot
// build at runtime; enabling them must surface the documented
// "restart required" outcome instead of silently doing nothing.
var restartOnlyTools = map[string]bool{
	"smartsearch": true, // BM25 index + change tracker are bootstrap-only
}

// TestMakeToolFactory_BuildsEveryConfigurableTool is the coverage table for
// bugs.md "Enabling a tool during a session does not enable it": every name in
// tools.ConfigurableTools() must either be constructible by makeToolFactory
// (schema name matches, and the Clarify hook is attached for the tool that
// needs one) or be one of the documented restart-only tools.
func TestMakeToolFactory_BuildsEveryConfigurableTool(t *testing.T) {
	subs := runtimeFactorySubsystems(t, factoryConfig())
	factory := makeToolFactory(subs)

	for _, ct := range tools.ConfigurableTools() {
		tool, ok := factory(ct.Name)
		if !ok {
			if !restartOnlyTools[ct.Name] {
				t.Errorf("factory cannot build %q and it is not documented as restart-only: enabling it at runtime would silently do nothing", ct.Name)
			}
			continue
		}
		if tool == nil {
			t.Errorf("factory reported success for %q but returned a nil tool", ct.Name)
			continue
		}
		if got := tool.Schema().Name; got != ct.Name {
			t.Errorf("factory(%q) built a tool whose schema name is %q", ct.Name, got)
		}
	}

	// The Clarify hook is the exclusive channel for ask_user_question: a
	// runtime-built instance must reach the user, not answer into the void.
	tool, ok := factory("ask_user_question")
	if !ok {
		t.Fatal("ask_user_question must be constructible at runtime")
	}
	askTool, ok := tool.(*ask.AskUserQuestionTool)
	if !ok {
		t.Fatalf("factory(ask_user_question) = %T, want *ask.AskUserQuestionTool", tool)
	}
	if askTool.Clarify == nil {
		t.Error("runtime-built ask_user_question has no Clarify hook: it cannot reach the user")
	}
}
