// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
	"path/filepath"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/core/swarm"
	"github.com/pijalu/goa/core/tasks"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/telemetry"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/prompts"
	"github.com/pijalu/goa/tools"
	toolsSwarm "github.com/pijalu/goa/tools/swarm"
	"github.com/pijalu/goa/tui"
)

func assembleSubsystems(cfg *config.Config, loader *config.CascadeLoader, projectDir string, base baseSubsystems, ab agentBundle, sc skillCommandBundle, promptReg *prompts.Registry, workflowReg *multiagent.WorkflowRegistry, agentPool *multiagent.AgentPool, foregroundOrch *multiagent.ForegroundOrchestrator, requestReviewTool *multiagent.RequestReviewTool, delegateTool *multiagent.DelegateTool, pipelineRunner *multiagent.PipelineRunner, goalManager *core.GoalManager, goalDriver *core.GoalDriver, opts RuntimeOptions, registry *core.CommandRegistry, swarmState *swarm.State, taskBus *tasks.Bus) *subsystems {
	promptDir := cfg.Prompts.Dir
	if promptDir == "" {
		promptDir = filepath.Join(projectDir, ".goa", "prompts")
	}
	// Per-model thinking-level changes made at runtime (/thinking, level
	// cycle shortcut, /config) are persisted through the config cascade.
	ab.agentMgr.SetConfigSaver(loader)
	s := &subsystems{
		cfg:               cfg,
		loader:            loader,
		worktreeMgr:       base.worktreeMgr,
		memStore:          base.memStore,
		sessionStore:      ab.sessionStore,
		skillRegistry:     sc.skillRegistry,
		promptReg:         promptReg,
		providerMgr:       base.providerMgr,
		modelValidator:    base.modelValidator,
		agentMgr:          ab.agentMgr,
		execCtrl:          ab.execCtrl,
		cmdRouter:         sc.cmdRouter,
		docEngine:         sc.docEngine,
		modeRegistry:      sc.modeRegistry,
		toolRegistry:      base.toolRegistry,
		pipelineRunner:    pipelineRunner,
		foregroundOrch:    foregroundOrch,
		workflowReg:       workflowReg,
		agentPool:         agentPool,
		ptyMgr:            base.ptyMgr,
		events:            ab.eventBus,
		goaTool:           sc.goaTool,
		swarmState:        swarmState,
		taskBus:           taskBus,
		projectDir:        projectDir,
		trustMgr:          base.trustMgr,
		commandStats:      NewCommandStats(projectDir),
		stateStore:        ab.stateStore,
		goalManager:       goalManager,
		goalDriver:        goalDriver,
		orchAdapter:       NewOrchestratorAdapter(agentPool, cfg, promptDir),
		teamManager:       newTeamManager(cfg, base.providerMgr, ab.agentMgr, agentPool, foregroundOrch, sc.modeRegistry, nil),
		orchActive:        orchestrator.NewActiveRuntime(),
		contextFiles:      internal.LoadProjectContextFiles(projectDir, cfg.ConfigDir),
		requestReviewTool: requestReviewTool,
		delegateTool:      delegateTool,
		logger:            ab.agentLogger,
		lifecycleRegistry: base.lifecycleRegistry,
		pluginMgr:         sc.pluginMgr,
		pluginHooks:       ab.pluginHooks,
		pluginSched:       ab.pluginSched,
		authStore:         sc.authStore,
		noPlugins:         opts.NoPlugins,
		MemoryEnabled:     !opts.NoMemory,
		MemoryBudget:      opts.MemoryBudget,
		perfLoad:          opts.PerfLoad,
		perfLoadDuration:  opts.PerfLoadDuration,
		agentStreams:      newAgentStreamRegistry(),
		registry:          registry,
		bgMgr:             base.bgMgr,
		lspMgr:            base.lspMgr,
		mcpManager:        base.mcpManager,
	}
	// Wire the goal drive loop's team-overlay manager now that both the goal
	// driver and the team manager exist (TEAMS.md §5.2: a team-bound goal
	// applies the team's overlay for its duration). The driver no-ops the
	// overlay when this is nil.
	s.goalDriver.TeamOverlay = s.teamManager
	// Team visibility (RC-4): every team transition announces itself in chat
	// and refreshes the footer badge so no team — session-level or goal
	// overlay — is ever hidden from the user.
	s.teamManager.SetChangeCallback(func(effective, reason string) {
		text := teamChangeAnnouncement(effective, reason)
		select {
		case s.events.Chat <- event.ChatEvent{Flash: &event.Flash{Text: text}}:
		default:
		}
		select {
		case s.events.Footer <- event.FooterEvent{FooterRefresh: true}:
		default:
		}
	})
	// Startup activation: teams.active from config is APPLIED here so the
	// configured team is real from the first turn — previously the config
	// value was inert and the team stayed hidden until manually activated.
	s.applyStartupTeam()
	// Re-bind the sticky skill provider to the assembled subsystems: it must
	// read the LIVE registry (subsystem field), because /reload swaps the
	// registry object and a provider pinned to the startup copy would keep
	// serving a stale sticky set after a /skill:sticky toggle.
	if ab.stickyProvider != nil {
		ab.stickyProvider.subs = s
	}
	if sc.goaTool != nil {
		sc.goaTool.SetContextFn(func() core.Context { return coreContextForCommand(s, nil) })
	}

	// Register the orchestrator slash command now that the adapter + active
	// holder exist. RegisterAll already ran (in initSkillAndCommandLayer) and
	// deliberately does not register /orchestrate unconditionally, so this is
	// the single registration point.
	s.orchAdapter.SetTelemetry(&telClientAdapter{client: telemetry.NewClient(opts.Telemetry, cfg.ConfigDir)})
	orchCmd := &commands.OrchestrateCommand{
		Builder:  s.orchAdapter,
		Active:   s.orchActive,
		RootDir:  filepath.Join(projectDir, ".goa", "orchestrator"),
		GoalMode: s.goalManager.Mode,
	}
	s.orchCmd = orchCmd
	orchCmd.ShowBrowser = func() {
		b := orchpanel.NewBrowser(orchCmd.RootDir, nil)
		handle := s.tuiEngine.ShowOverlay(b, tui.OverlayOptions{CaptureInput: true})
		b.SetCloseFunc(func() { handle.Hide() })
	}
	_ = s.registry.Register(orchCmd)

	// /agent: the multi-agent tab surface (T5). Registered here — not in
	// RegisterAll — because its host callbacks need the assembled
	// subsystems, exactly like /orchestrate. The callbacks execute at
	// command time (long after assembly), so binding them through a throw-
	// away App over the same subsystems pointer is safe.
	_ = s.registry.Register((&App{subs: s}).newAgentCommand())

	// Wire the plan command with execution support now that the adapter exists.
	if cmd, ok := s.registry.Resolve("plan"); ok {
		if planCmd, ok := cmd.(*commands.PlanCommand); ok {
			binder := NewPlanBinder(s.orchAdapter, cfg, projectDir, filepath.Join(projectDir, ".goa", "prompts"), s.events)
			binder.BindPlanCommand(planCmd)
		}
	}

	s.dreamScheduler = newDreamScheduler(s)
	_ = s.dreamScheduler.readSchedulerState()
	s.dreamScheduler.Start()
	wireDreamScheduler(s.agentMgr, s.dreamScheduler)

	if s.modelValidator != nil {
		s.modelValidator.Start(context.Background(), 5*time.Minute)
	}

	s.startOrchestratorCleanup()

	return s
}

// modeResolverAdapter wraps core.ModeRegistry to implement multiagent.ModeResolver,
// allowing AgentTool and AgentSwarmTool to resolve mode definitions for sub-agents
// without importing the core package directly.
type modeResolverAdapter struct {
	reg *core.ModeRegistry
}

func (a *modeResolverAdapter) Resolve(major string) (multiagent.ModeSpec, error) {
	spec, err := a.reg.Resolve(internal.MajorMode(major))
	if err != nil {
		return multiagent.ModeSpec{}, err
	}
	return multiagent.ModeSpec{
		Name:         spec.Name,
		Body:         spec.Body,
		AllowedTools: spec.AllowedTools,
		Temperature:  spec.Temperature,
	}, nil
}

// registerSubAgentTools creates and registers AgentTool and AgentSwarmTool
// with the tool registry, providing them with the AgentPool and ModeResolver
// needed to spawn sub-agents with mode-appropriate configuration. Each tool
// is registered only when enabled via tools.enabled (default: enabled).
func registerSubAgentTools(reg *tools.ToolRegistry, pool *multiagent.AgentPool, modeRegistry *core.ModeRegistry, swarmState *swarm.State, taskBus *tasks.Bus, agentMgr *core.AgentManager, eventBus *event.Bus, cfg *config.Config) {
	if cfg.Tools.Enabled.Agent {
		reg.Register(newAgentTool(pool, modeRegistry, taskBus, agentMgr))
	}
	if cfg.Tools.Enabled.AgentSwarm {
		reg.Register(newAgentSwarmTool(pool, modeRegistry, swarmState, taskBus, agentMgr, eventBus))
	}
}

// currentModeFunc returns a resolver for the agent manager's current mode,
// tolerating a nil manager (tests, partially assembled subsystems).
func currentModeFunc(agentMgr *core.AgentManager) func() internal.ModeState {
	return func() internal.ModeState {
		if agentMgr == nil {
			return internal.ModeState{}
		}
		return agentMgr.CurrentMode()
	}
}

// newAgentTool builds the `agent` sub-agent tool. Shared by initial
// registration and runtime re-enable via the tool factory.
func newAgentTool(pool *multiagent.AgentPool, modeRegistry *core.ModeRegistry, taskBus *tasks.Bus, agentMgr *core.AgentManager) *multiagent.AgentTool {
	return &multiagent.AgentTool{
		Pool:         pool,
		ModeResolver: &modeResolverAdapter{reg: modeRegistry},
		TaskBus:      taskBus,
		CurrentMode:  currentModeFunc(agentMgr),
	}
}

// newAgentSwarmTool builds the `agent_swarm` tool. Shared by initial
// registration and runtime re-enable via the tool factory.
func newAgentSwarmTool(pool *multiagent.AgentPool, modeRegistry *core.ModeRegistry, swarmState *swarm.State, taskBus *tasks.Bus, agentMgr *core.AgentManager, eventBus *event.Bus) *toolsSwarm.AgentSwarmTool {
	return &toolsSwarm.AgentSwarmTool{
		Pool:         pool,
		ModeResolver: &modeResolverAdapter{reg: modeRegistry},
		TaskBus:      taskBus,
		SwarmState:   swarmState,
		CurrentMode:  currentModeFunc(agentMgr),
		Emitter:      &swarmEmitter{bus: eventBus},
	}
}
