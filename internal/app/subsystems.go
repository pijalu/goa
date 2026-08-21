// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"log"
	"sync"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	commands "github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/core/swarm"
	"github.com/pijalu/goa/core/tasks"
	"github.com/pijalu/goa/core/team"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/auth"
	"github.com/pijalu/goa/internal/background"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/lsp"
	"github.com/pijalu/goa/internal/mcp"
	"github.com/pijalu/goa/internal/tooltracker"
	"github.com/pijalu/goa/internal/trust"
	"github.com/pijalu/goa/memory"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/plugins"
	"github.com/pijalu/goa/prompts"
	"github.com/pijalu/goa/provider"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tui"
	"github.com/pijalu/goa/tui/agentctx"
	bgpanel "github.com/pijalu/goa/tui/background"
	goaltui "github.com/pijalu/goa/tui/goal"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
)

// subsystems bundles all initialized subsystems for clean return from InitSubsystems.
type subsystems struct {
	cfg            *config.Config
	loader         *config.CascadeLoader
	worktreeMgr    *internal.WorktreeManager
	memStore       *memory.MemoryStore
	sessionStore   *core.SessionStore
	skillRegistry  *skills.SkillRegistry
	promptReg      *prompts.Registry
	providerMgr    *provider.ProviderManager
	modelValidator *provider.ModelValidator
	agentMgr       *core.AgentManager
	execCtrl       *core.ExecutionController
	cmdRouter      *core.CommandRouter
	docEngine      *core.DocEngine
	modeRegistry   *core.ModeRegistry
	toolRegistry   *tools.ToolRegistry
	ptyMgr         *internal.PTYManager
	pipelineRunner *multiagent.PipelineRunner
	foregroundOrch *multiagent.ForegroundOrchestrator
	workflowReg    *multiagent.WorkflowRegistry
	agentPool      *multiagent.AgentPool
	teamManager    *team.Manager
	events         *event.Bus
	goaTool        *core.GoaCommandTool // retained so /tools:goa:on can re-register at runtime
	swarmState     *swarm.State         // retained so /tools:agent_swarm:on can rebuild the tool
	taskBus        *tasks.Bus           // retained so /tools:agent:on can rebuild the tool
	projectDir     string
	inputEditor    *tui.Editor // the input line, set after buildTUI
	// cmdCompleter backs the editor's /command completion. Retained so plugin
	// commands (e.g. /quota), registered by the async plugin load after the
	// TUI is built, can be pushed into the live completer.
	cmdCompleter      *tui.CommandCompleter
	commandStats      *CommandStats
	stateStore        *core.StateStore
	goalManager       *core.GoalManager
	goalDriver        *core.GoalDriver
	orchAdapter       *OrchestratorAdapter
	orchActive        *orchestrator.ActiveRuntime
	orchCmd           *commands.OrchestrateCommand
	trustMgr          *trust.Manager
	authStore         *auth.Store
	lifecycleRegistry *plugins.LifecycleRegistry
	pluginMgr         *plugins.Manager
	// pluginRT holds loaded plugin bridges, set by loadEnabledPlugins. It is
	// written on the async plugin-load goroutine and read on the command loop,
	// so access is guarded by pluginRTMu.
	pluginRTMu sync.RWMutex
	pluginRT   *pluginRuntime
	noPlugins  bool // --no-plugins: skip plugin load entirely
	// sessionUsageFn supplies cumulative token stats to plugins (goa.sessionUsage).
	// Wired in New() once the App (which owns the counters) exists.
	sessionUsageFn func() map[string]any
	runWizard      bool // set when /setup command requests wizard

	// TUI components (set after InitSubsystems)
	chat           *tui.ChatViewport
	// agentRegistry owns the per-agent view contexts (AgentTranscript +
	// saved compositor state), keyed by agent id. In T1 it holds exactly the
	// main agent; chat above is agentRegistry's main view's ChatViewport, so
	// every existing chat.* call site is byte-for-byte unchanged. Multi-agent
	// switching (T2+) adds sub-agent views here.
	agentRegistry  *agentctx.AgentViewRegistry
	goalBubble     *goaltui.Bubble
	steeringChrome *tui.SteeringChrome
	footer         *tui.Footer
	tuiEngine      *tui.TUI
	bgPanel        *bgpanel.Panel

	// Logger for structured stats output
	logger    *agentic.Logger
	statusMsg *tui.StatusMsg

	// Perf-load mode settings.
	perfLoad         bool
	perfLoadDuration time.Duration

	// toolTracker owns the lifecycle of tool-call widgets for the foreground
	// conversation stream (the single source of truth — no per-call maps).
	toolTracker *tooltracker.Tracker

	// Context files (AGENTS.md) loaded at startup
	contextFiles []internal.ContextFile

	// MemoryEnabled controls whether long-term memory is injected into the
	// system prompt. It is set from runtime options and applies to both TUI
	// and headless modes.
	MemoryEnabled bool

	// MemoryBudget limits the tokens injected from memory summaries. 0 means
	// automatic (1024 tokens or 10% of context window, whichever is smaller).
	MemoryBudget int

	// ContextWindow overrides the active model's context window for budget
	// calculations. It is set from local provider detection when available.
	ContextWindow int

	// Agent-driven tool instances (kept so toggles can update Enabled)
	requestReviewTool *multiagent.RequestReviewTool
	delegateTool      *multiagent.DelegateTool

	// Persistent tabbed multi-agent run view (replaces the transient panel
	// overlay). agentView is the single state owner mutated only on the command
	// loop; agentContent and agentTabBar are render-only views over it. All
	// three are nil/unattached outside an active run.
	agentView    *orchpanel.MultiAgentView
	agentContent *orchpanel.AgentContent
	agentTabBar  *orchpanel.AgentTabBar

	// agentStreams tracks per-agent streaming state for orchestrator
	// conversation rendering in the main chat viewport.
	agentStreams *agentStreamRegistry

	// dreamScheduler triggers automatic memory consolidation after sessions.
	dreamScheduler *dreamScheduler

	// registry holds the explicitly-injected command registry used across the
	// app. Replaces the deprecated core.GlobalRegistry package variable.
	registry *core.CommandRegistry

	// Background task manager shared by the bg_exec tool and the status panel.
	bgMgr *background.Manager

	// lspMgr runs gopls for Go diagnostics; closed on shutdown to avoid leaks.
	lspMgr *lsp.Manager
	// mcpManager owns MCP server connections and their registered tools;
	// closed on shutdown. Nil when no MCP servers are configured.
	mcpManager *mcp.Manager

	// configWatcher watches the writable config cascade layers and hot-applies
	// reloaded provider profiles. Started for the interactive TUI session and
	// closed on shutdown (no goroutine leaks). configWatchWG tracks the change
	// consumer goroutine so stopConfigWatcher can wait for it to exit.
	configWatcher *config.ConfigWatcher
	configWatchWG sync.WaitGroup
}

func (s *subsystems) getInput() *tui.Editor { return s.inputEditor }

// liveConfig returns the config for the next request: the hot-reloaded config
// once the config watcher has published one, otherwise the boot config. The
// request path (StartSession) must read through here — never the static
// subs.cfg — so an external config edit applies on the next request without a
// restart (P22/DS6).
func (s *subsystems) liveConfig() *config.Config {
	if s == nil {
		return nil
	}
	if s.providerMgr != nil {
		if c := s.providerMgr.Config(); c != nil {
			return c
		}
	}
	return s.cfg
}

// startConfigWatcher begins watching the writable config cascade layers and
// hot-applies reloaded provider profiles. It is enabled only for the
// interactive TUI session, where the acceptance path lives; one-shot modes
// (headless, ACP, dream, export) run a single request and close before a
// hot reload could matter. Idempotent.
func (s *subsystems) startConfigWatcher() {
	if s == nil || s.configWatcher != nil || s.loader == nil || s.providerMgr == nil {
		return
	}
	w, err := config.NewConfigWatcher(s.loader, log.Printf)
	if err != nil {
		log.Printf("config hot-reload disabled: %v", err)
		return
	}
	w.Start()
	s.configWatcher = w
	s.configWatchWG.Add(1)
	go func() {
		defer s.configWatchWG.Done()
		for cfg := range w.Changes() {
			s.applyReloadedConfig(cfg)
		}
	}()
}

// stopConfigWatcher shuts the watcher down and waits for the consumer
// goroutine to exit, so no goroutine leaks across restarts. Idempotent.
func (s *subsystems) stopConfigWatcher() {
	if s == nil || s.configWatcher == nil {
		return
	}
	s.configWatcher.Close()
	s.configWatchWG.Wait()
	s.configWatcher = nil
}

// applyReloadedConfig swaps the live provider profile to a freshly reloaded
// config so the next request resolves the new provider/model/effort. The
// boot-time subs.cfg is intentionally not replaced: it is read from many
// goroutines without synchronization, and the request path already reads the
// live config via liveConfig.
func (s *subsystems) applyReloadedConfig(cfg *config.Config) {
	if cfg == nil {
		return
	}
	if s.providerMgr != nil {
		s.providerMgr.SetConfig(cfg)
	}
	if s.agentPool != nil {
		s.agentPool.SetGoaConfig(cfg)
	}
	log.Printf("config hot-reloaded: provider profile updated from disk")
}

// effectiveModeState returns the live session mode — the value restored from
// state.json on startup or changed at runtime via /mode — falling back to the
// configured default when no session is active. Every UI surface (footer,
// prompt, status) must read the live mode rather than the static config
// default, otherwise a mode change that was persisted to state.json is
// invisible after a restart (the footer would keep showing the config's
// mode.default.major instead of the restored runtime mode).
func (s *subsystems) effectiveModeState() internal.ModeState {
	if s != nil && s.agentMgr != nil {
		if m := s.agentMgr.CurrentMode(); !m.IsZero() {
			return m
		}
	}
	if s != nil && s.cfg != nil {
		return s.cfg.DefaultModeState()
	}
	return internal.ModeState{}
}

// InitSubsystems wires together all of Goa's subsystems from a loaded config
// and runtime options.
func InitSubsystems(cfg *config.Config, loader *config.CascadeLoader, projectDir string, opts RuntimeOptions) *subsystems {
	subs := initBaseSubsystems(cfg, projectDir, opts.Headless())
	agentBundle := initAgentBundle(cfg, projectDir)
	initHookEngine(cfg, projectDir, agentBundle.agentMgr)

	// Scheduler delivery (P18/TL2): due jobs become user messages at the
	// start of the next turn (user turns and goal continuation turns alike).
	wireScheduleDelivery(agentBundle.agentMgr, subs.scheduleStore)

	// Steering queue: shared between AgentManager (consumes at turn end) and
	// TUI submit handler (appends while a turn is running).
	steeringQueue := core.NewSteeringQueue()
	agentBundle.agentMgr.SetSteeringQueue(steeringQueue)

	if cfg.MultiAgent.MessageTimeout != "" {
		if d, err := time.ParseDuration(cfg.MultiAgent.MessageTimeout); err == nil {
			agentBundle.agentMgr.SetCompanionTimeout(d)
		}
	}

	agentBundle.agentMgr.SetLifecycleRegistry(subs.lifecycleRegistry)
	agentBundle.agentMgr.SetContextWindowRefresher(func() int {
		if subs.providerMgr == nil {
			return 0
		}
		return subs.providerMgr.RefreshLocalContextWindow()
	})
	// Shared swarm state + task bus: created once so the /swarm command, the
	// agent_swarm tool, the agent (sub-agent) tool, and the system-prompt
	// reminder injector all observe the same state. Previously each was nil
	// in production, leaving swarm mode and background task tracking no-ops.
	swarmState := swarm.NewState()
	taskBus := tasks.NewBus(tasks.NopStore{}, agentBundle.eventBus)
	goalManager, goalDriver := initGoalSystem(cfg, projectDir, agentBundle.eventBus, agentBundle.agentMgr, swarmState, subs.providerMgr)
	// Goal tools are always registered (stable tool array, S2). The
	// tools.enabled.goal flag gates only AUTONOMOUS creation at execution time:
	// `create` is allowed when the flag is on OR a goal is already active.
	registerGoalTools(subs.toolRegistry, goalManager, cfg.Tools.Enabled.Goal || opts.Goal, cfg.Goals.AutoUnblockEnabled, cfg.Goals.FreshContextEnabled,
		func() time.Duration { return cfg.Goals.VerifyTimeoutOr(defaultGoalVerifyTimeout) })
	// The standalone todo_list tool (available outside of goal). It is
	// linked to the goal's own todo list while a goal is active and falls back
	// to its session list otherwise; tools.enabled.todo gates registration.
	if cfg.Tools.Enabled.Todo {
		subs.toolRegistry.Register(&tools.TodoListTool{Mode: goalManager.Mode})
	}
	registerWebFetchTool(subs.toolRegistry, agentBundle.sessionStore, cfg, projectDir)
	registerSessionQueryTools(subs.toolRegistry, agentBundle.sessionStore)
	registry := core.NewCommandRegistry()
	skillBundle := initSkillAndCommandLayer(cfg, projectDir, subs.providerMgr, subs.toolRegistry, goalManager, goalDriver, agentBundle.agentMgr, subs.trustMgr, opts.Telemetry, swarmState, registry, !opts.NoPlugins)
	promptReg, workflowReg := initPromptAndWorkflowLayer(cfg, projectDir)
	modeRegistry := core.NewModeRegistry(promptReg)
	loadUserModes(modeRegistry, cfg.ConfigDir, projectDir)
	skillBundle.modeRegistry = modeRegistry
	agentBundle.agentMgr.SetModeRegistry(modeRegistry)
	populateModeDefaults(cfg, modeRegistry)

	// The bash tool's Jail flag is initialised from the config default during
	// tool registration (which runs before state.json is loaded). Re-apply it
	// from the restored runtime autonomy so a persisted SOLO session keeps the
	// jail enabled after a restart.
	makeJailSetter(subs.toolRegistry)(agentBundle.agentMgr.CurrentMode().Autonomy == internal.AutonomySolo)

	var agentPool *multiagent.AgentPool
	var foregroundOrch *multiagent.ForegroundOrchestrator
	var requestReviewTool *multiagent.RequestReviewTool
	var delegateTool *multiagent.DelegateTool
	if subs.providerMgr != nil {
		if mdl, err := subs.providerMgr.ResolveActiveModel(); err == nil {
			agentDrivenTools := multiagent.AgentDrivenTools(nil, nil)
			// Companion intent (restored companion minor mode / agent-driven flag)
			// must win over a persisted tools.enabled false: existing configs
			// serialized the old default as an explicit false, which otherwise
			// keeps request_review/delegate_to unregistered even when the user
			// explicitly enabled companion mode (e2e T2 bug).
			companionActive := agentBundle.stateSnapshot.AgentDrivenEnabled ||
				agentBundle.stateSnapshot.MinorMode == "companion"
			requestReviewTool, delegateTool = registerAgentDrivenTools(subs.toolRegistry, agentDrivenTools, cfg, companionActive)
			agentPool = createAgentPool(mdl, subs.providerMgr, subs.toolRegistry, promptReg, cfg, modeRegistry, swarmState, taskBus, agentBundle.agentMgr, agentBundle.eventBus)
			foregroundOrch = wireForegroundOrchestrator(agentPool, promptReg, agentBundle.agentMgr, cfg, workflowReg)
			agentPool.SetOrchestrator(foregroundOrch)
			wireCacheStatsIdentity(foregroundOrch, agentBundle.agentMgr, goalManager)
			wireCompanionCreation(agentPool, agentBundle.agentMgr, agentBundle.stateSnapshot)
			registerSkillRunner(subs.toolRegistry, skillBundle.skillRegistry, agentPool, promptReg, cfg)
			registerBuiltinWorkflows(workflowReg)
			restoreSessionState(agentBundle.agentMgr, agentBundle.stateSnapshot, requestReviewTool, delegateTool, cfg)
			wireAgentBus(agentBundle.agentMgr, agentPool, foregroundOrch, cfg.MultiAgent.MaxCompanionCycles)
			attachAgentDrivenToolPools(agentDrivenTools, agentPool)
			// Sticky knowledge skills (sticky: true frontmatter): always-on
			// instruction blocks persisted into every agent's history — main
			// agent and all pool-created sub-agents ("new context" agents).
			// They survive /new (fresh persist), session restore (history-scan
			// dedup), and context compression (re-persist via emitCompaction).
			sticky := &stickySkillProvider{}
			agentBundle.stickyProvider = sticky
			agentBundle.agentMgr.SetStickyProvider(sticky)
			agentPool.SetStickyProvider(sticky)
		}
	}

	pipelineRunner := multiagent.NewPipelineRunner()
	if agentPool != nil {
		pipelineRunner.SetAgentPool(agentPool)
		attachWebFetchSummarizer(subs.toolRegistry, &webFetchAgentPool{pool: agentPool})
	}

	return assembleSubsystems(cfg, loader, projectDir, subs, agentBundle, skillBundle, promptReg, workflowReg, agentPool, foregroundOrch, requestReviewTool, delegateTool, pipelineRunner, goalManager, goalDriver, opts, registry, swarmState, taskBus)
}

// wireCacheStatsIdentity connects cache-stats identity: sub-agent turns flow
// into the session turn recorder (per-agent /stats:cache sections), and the
// main agent's turns are tagged with the active goal. The goal ID is
// resolved at emit/finalize time from the live goal mode.
func wireCacheStatsIdentity(orch *multiagent.ForegroundOrchestrator, agentMgr *core.AgentManager, goalManager *core.GoalManager) {
	activeGoalID := func() string {
		if g := goalManager.Mode.GetActiveGoal(); g != nil {
			return g.GoalID
		}
		return ""
	}
	orch.SetCacheStatsCallback(func(role, goalID string, u multiagent.SubAgentCacheUsage) {
		agentMgr.TurnRecorder().RecordSubAgentTurn(role, goalID, core.TurnTokenUsage{
			PromptN:    u.PromptN,
			PredictedN: u.PredictedN,
			CacheRead:  u.CacheRead,
			CacheWrite: u.CacheWrite,
		})
	}, activeGoalID)
	agentMgr.SetActiveGoalIDProvider(activeGoalID)
}
