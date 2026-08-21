// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"log"
	"path/filepath"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/core/sessiontree"
	"github.com/pijalu/goa/core/swarm"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/auth"
	"github.com/pijalu/goa/internal/telemetry"
	"github.com/pijalu/goa/internal/trust"
	"github.com/pijalu/goa/internal/update"
	"github.com/pijalu/goa/internal/version"
	"github.com/pijalu/goa/plugins"
	"github.com/pijalu/goa/provider"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tools"
)

func initSkillAndCommandLayer(cfg *config.Config, projectDir string, providerMgr *provider.ProviderManager, toolRegistry *tools.ToolRegistry, goalManager *core.GoalManager, goalDriver *core.GoalDriver, agentMgr *core.AgentManager, trustMgr *trust.Manager, telemetryEnabled bool, swarmState *swarm.State, registry *core.CommandRegistry, pluginsEnabled bool) skillCommandBundle {
	cfgDir := cfg.ConfigDir
	if cfgDir == "" {
		cfgDir = filepath.Join(projectDir, ".goa")
	}
	pluginRoot := filepath.Join(cfgDir, "plugins")
	pluginMgr, err := plugins.NewManager(pluginRoot, trustMgr)
	if err != nil {
		log.Printf("Warning: failed to create plugin manager: %v\n", err)
	}

	skillRegistry := newSkillRegistry(cfg, projectDir, pluginMgr, pluginsEnabled, trustMgr)

	goalCmd := &commands.GoalCommand{
		Mode:                goalManager.Mode,
		Queue:               goalManager.Queue,
		Driver:              goalDriver,
		AutonomySwitcher:    newAutonomySwitcher(agentMgr, cfg, makeJailSetter(toolRegistry)),
		FreshContextDefault: cfg.Goals.FreshContextEnabled,
	}
	// Wire the queue as the goal name pool so newly created active goals pick
	// a friendly alias that does not collide with queued goals.
	goalManager.Mode.SetNamePool(goalManager.Queue)
	authStore, err := auth.NewStore(filepath.Join(cfgDir, "auth.json"))
	if err != nil {
		log.Printf("Warning: auth store unavailable, falling back to in-memory: %v", err)
		authStore, _ = auth.NewStore("")
	}

	if providerMgr != nil {
		providerMgr.SetAuthStore(authStore)
	}
	sessTree := sessiontree.NewManager(sessiontree.NewJSONStore(filepath.Join(cfgDir, "session-tree.json")))
	themeStore := config.NewThemeStore(filepath.Join(cfgDir, "themes"))
	currentVer := version.Version()
	updateChecker := update.NewChecker(currentVer, cfgDir)
	telClient := telemetry.NewClient(telemetryEnabled, cfgDir)

	deps := commands.CommandDependencies{
		GoalCommand:     goalCmd,
		AuthStore:       authStore,
		PluginManager:   pluginMgr,
		SessionTree:     sessTree,
		ThemeStore:      themeStore,
		UpdateChecker:   updateChecker,
		TelemetryClient: telClient,
		TrustManager:    trustMgr,
		SwarmState:      swarmState,
	}
	if err := commands.RegisterAll(registry, deps); err != nil {
		log.Fatalf("Failed to register commands: %v", err)
	}
	if warnings := commands.RegisterSkillShortcuts(registry, skillRegistry); len(warnings) > 0 {
		for _, w := range warnings {
			log.Printf("Warning: %s\n", w)
		}
	}

	docEngine := core.NewDocEngine(registry)
	docEngine.SetToolRegistry(toolRegistry)
	docEngine.SetSkillRegistry(skillRegistry)
	cmdRouter := core.NewCommandRouter(registry, docEngine)
	if cfg.Aliases != nil {
		cmdRouter.SetAliases(cfg.Aliases)
	}

	goaTool := registerGoaCommandTool(cmdRouter, toolRegistry, cfg)

	return skillCommandBundle{
		skillRegistry: skillRegistry,
		docEngine:     docEngine,
		cmdRouter:     cmdRouter,
		goaTool:       goaTool,
		pluginMgr:     pluginMgr,
		authStore:     authStore,
	}
}

// newSkillRegistry assembles the skill directories (defaults + configured +
// enabled-plugin skills) and loads the registry. Plugin skill dirs are
// included only when plugins are enabled (--no-plugins skips them).
func newSkillRegistry(cfg *config.Config, projectDir string, pluginMgr *plugins.Manager, pluginsEnabled bool, trustMgr *trust.Manager) *skills.SkillRegistry {
	skillDirs := append(config.DefaultSkillDirs(projectDir), cfg.Skills.Dirs...)
	if pluginMgr != nil && pluginsEnabled {
		skillDirs = append(skillDirs, pluginMgr.EnabledSkillDirs()...)
	}
	skillRegistry := skills.NewSkillRegistry(skillDirs)
	skillRegistry.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	skillRegistry.SetTrustChecker(newSkillTrustChecker(trustMgr))
	skillRegistry.SetDisabled(cfg.Skills.Disabled)
	skillRegistry.SetEnabled(cfg.Skills.Enabled)
	// Embedded skills are OFF by default except telegram; the user
	// opts individual ones back in via skills.embedded_enabled (or the global
	// allowlist). File-based skills are never affected by the default-off set.
	skillRegistry.SetEmbeddedDefaultDisabled(skills.DefaultEmbeddedOffNames(skills.EmbeddedSkillsFS))
	skillRegistry.SetEmbeddedEnabled(cfg.Skills.EmbeddedEnabled)
	// Config-level sticky overrides (skills.sticky / skills.sticky_off),
	// persisted at project level and toggled via /skill:sticky and /config.
	skillRegistry.SetStickyOverrides(cfg.Skills.Sticky, cfg.Skills.StickyOff)
	if err := skillRegistry.LoadAll(); err != nil {
		log.Printf("Warning: failed to load skills: %v\n", err)
	} else if n := len(skillRegistry.List()); n > 0 {
		log.Printf("Loaded %d skills from %d directories\n", n, len(skillDirs))
	}
	return skillRegistry
}

// registerGoaCommandTool creates the goa_command tool now that the command
// router exists and registers it unless disabled via tools.enabled.goa
// (default on). The execution context is wired later in assembleSubsystems
// once the subsystems are fully assembled.
func registerGoaCommandTool(cmdRouter *core.CommandRouter, toolRegistry *tools.ToolRegistry, cfg *config.Config) *core.GoaCommandTool {
	goaTool := core.NewGoaCommandToolWithContextFn(cmdRouter, func() core.Context { return core.Context{} })
	if cfg.Tools.Enabled.Goa {
		toolRegistry.Register(goaTool)
	}
	return goaTool
}

// loadUserModes discovers custom modes from .goa/prompts/mode/ in the home
// and project directories and registers them with the mode registry.
func loadUserModes(registry *core.ModeRegistry, dirs ...string) {
	// Custom modes are already discovered at startup by the prompts registry:
	// prompts.Registry.ListModes() walks both embedded and user directories
	// (via collectUserModes), and ModeRegistry.loadBuiltins() loads them all.
	// No additional work is needed here.
}

// populateModeDefaults fills cfg.Mode.Defaults from the mode registry so that
// modes defined purely in metadata (e.g. prompts/mode/coding-posture) do not
// need a code change in config.DefaultAutonomyForMajor.
func populateModeDefaults(cfg *config.Config, registry *core.ModeRegistry) {
	if registry == nil {
		return
	}
	if cfg.Mode.Defaults == nil {
		cfg.Mode.Defaults = make(map[internal.MajorMode]internal.AutonomyLevel)
	}
	for _, major := range registry.Majors() {
		spec, err := registry.Resolve(major)
		if err != nil || spec.DefaultAutonomy == "" {
			continue
		}
		if _, ok := cfg.Mode.Defaults[major]; !ok {
			cfg.Mode.Defaults[major] = spec.DefaultAutonomy
		}
	}
}

// TrustManager returns the subsystems trust manager.
func (s *subsystems) TrustManager() *trust.Manager {
	return s.trustMgr
}

// LifecycleRegistry returns the plugin lifecycle registry.
func (s *subsystems) LifecycleRegistry() *plugins.LifecycleRegistry {
	return s.lifecycleRegistry
}

type skillCommandBundle struct {
	skillRegistry *skills.SkillRegistry
	docEngine     *core.DocEngine
	cmdRouter     *core.CommandRouter
	modeRegistry  *core.ModeRegistry
	goaTool       *core.GoaCommandTool
	pluginMgr     *plugins.Manager
	authStore     *auth.Store
}
