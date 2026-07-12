// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/orchestrator"
	commands "github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/core/goal"
	"github.com/pijalu/goa/core/sessiontree"
	"github.com/pijalu/goa/core/swarm"
	"github.com/pijalu/goa/core/tasks"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	agenticprovider "github.com/pijalu/goa/internal/agentic/provider"
	"github.com/pijalu/goa/internal/auth"
	"github.com/pijalu/goa/internal/background"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/hooks"
	"github.com/pijalu/goa/internal/role"
	"github.com/pijalu/goa/internal/sandbox"
	"github.com/pijalu/goa/internal/telemetry"
	"github.com/pijalu/goa/internal/trust"
	"github.com/pijalu/goa/internal/update"
	"github.com/pijalu/goa/internal/version"
	"github.com/pijalu/goa/memory"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/plugins"
	"github.com/pijalu/goa/prompts"
	"github.com/pijalu/goa/provider"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tools"
	toolsSwarm "github.com/pijalu/goa/tools/swarm"
	"github.com/pijalu/goa/tui"
	goaltui "github.com/pijalu/goa/tui/goal"
	bgpanel "github.com/pijalu/goa/tui/background"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
)

// subsystems bundles all initialized subsystems for clean return from InitSubsystems.
type subsystems struct {
	cfg               *config.Config
	loader            *config.CascadeLoader
	worktreeMgr       *internal.WorktreeManager
	memStore          *memory.MemoryStore
	sessionStore      *core.SessionStore
	skillRegistry     *skills.SkillRegistry
	promptReg         *prompts.Registry
	providerMgr       *provider.ProviderManager
	modelValidator    *provider.ModelValidator
	agentMgr          *core.AgentManager
	execCtrl          *core.ExecutionController
	cmdRouter         *core.CommandRouter
	docEngine         *core.DocEngine
	modeRegistry      *core.ModeRegistry
	toolRegistry      *tools.ToolRegistry
	ptyMgr            *internal.PTYManager
	pipelineRunner    *multiagent.PipelineRunner
	foregroundOrch    *multiagent.ForegroundOrchestrator
	workflowReg       *multiagent.WorkflowRegistry
	agentPool         *multiagent.AgentPool
	events            *event.Bus
	projectDir        string
	inputEditor       *tui.Editor // the input line, set after buildTUI
	commandStats      *CommandStats
	stateStore        *core.StateStore
	goalManager       *core.GoalManager
	goalDriver        *core.GoalDriver
	orchAdapter       *OrchestratorAdapter
	orchActive        *orchestrator.ActiveRuntime
	trustMgr          *trust.Manager
	lifecycleRegistry *plugins.LifecycleRegistry
	pluginMgr         *plugins.Manager
	runWizard         bool // set when /setup command requests wizard

	// TUI components (set after InitSubsystems)
	chat       *tui.ChatViewport
	goalBubble *goaltui.Bubble
	footer     *tui.Footer
	tuiEngine  *tui.TUI
	bgPanel    *bgpanel.Panel

	// Logger for structured stats output
	logger      *agentic.Logger
	statusMsg   *tui.StatusMsg
	pendingMsgs *tui.StatusMsg

	// Perf-load mode settings.
	perfLoad         bool
	perfLoadDuration time.Duration

	// Active tool components for expand/collapse, keyed by ToolCallID so that
	// concurrently-executing tool results update the correct component. The
	// legacy single-slot field is kept as a fallback for events without an ID.
	activeTools map[string]*tui.ToolExecutionComponent
	activeTool  *tui.ToolExecutionComponent

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

	// Orchestrator Summary panel and its overlay handle. Set while an
	// orchestration run is active so the main input line can be contextual.
	orchPanel       *orchpanel.Panel
	orchPanelHandle *tui.OverlayHandle

	// dreamScheduler triggers automatic memory consolidation after sessions.
	dreamScheduler *dreamScheduler

	// registry holds the explicitly-injected command registry used across the
	// app. Replaces the deprecated core.GlobalRegistry package variable.
	registry *core.CommandRegistry

	// Background task manager shared by the bg_exec tool and the status panel.
	bgMgr *background.Manager
}

func (s *subsystems) getInput() *tui.Editor { return s.inputEditor }

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
	subs := initBaseSubsystems(cfg, projectDir)
	agentBundle := initAgentBundle(cfg, projectDir)
	initHookEngine(cfg, projectDir, agentBundle.agentMgr)

	// Steering queue: shared between AgentManager (consumes at turn end) and
	// TUI submit handler (appends while a turn is running).
	steeringQueue := core.NewSteeringQueue()
	agentBundle.agentMgr.SetSteeringQueue(steeringQueue)

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
	goalManager, goalDriver := initGoalSystem(projectDir, agentBundle.eventBus, agentBundle.agentMgr, swarmState)
	if cfg.Tools.Enabled.Goal || opts.Goal {
		registerGoalTools(subs.toolRegistry, goalManager)
	}
	registerWebFetchTool(subs.toolRegistry, agentBundle.sessionStore, cfg, projectDir)
	registry := core.NewCommandRegistry()
	skillBundle := initSkillAndCommandLayer(cfg, projectDir, subs.toolRegistry, goalManager, goalDriver, agentBundle.agentMgr, subs.trustMgr, opts.Telemetry, swarmState, registry)
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
			requestReviewTool, delegateTool = registerAgentDrivenTools(subs.toolRegistry, agentDrivenTools, cfg)
			agentPool = createAgentPool(mdl, subs.providerMgr, subs.toolRegistry, promptReg, cfg, modeRegistry, swarmState, taskBus, agentBundle.agentMgr)
			foregroundOrch = wireForegroundOrchestrator(agentPool, promptReg, agentBundle.agentMgr, cfg, workflowReg)
			agentPool.SetOrchestrator(foregroundOrch)
			wireCompanionCreation(agentPool, agentBundle.agentMgr, agentBundle.stateSnapshot)
			registerSkillRunnerIfNeeded(subs.toolRegistry, skillBundle.skillRegistry, agentPool, promptReg, cfg)
			registerBuiltinWorkflows(workflowReg)
			restoreSessionState(agentBundle.agentMgr, agentBundle.stateSnapshot, requestReviewTool, delegateTool, cfg)
			wireAgentBus(agentBundle.agentMgr, agentPool, foregroundOrch, cfg.MultiAgent.MaxCompanionCycles)
			attachAgentDrivenToolPools(agentDrivenTools, agentPool)
		}
	}

	pipelineRunner := multiagent.NewPipelineRunner()
	if agentPool != nil {
		pipelineRunner.SetAgentPool(agentPool)
		attachWebFetchSummarizer(subs.toolRegistry, &webFetchAgentPool{pool: agentPool})
	}

	return assembleSubsystems(cfg, loader, projectDir, subs, agentBundle, skillBundle, promptReg, workflowReg, agentPool, foregroundOrch, requestReviewTool, delegateTool, pipelineRunner, goalManager, goalDriver, opts, registry)
}

func initBaseSubsystems(cfg *config.Config, projectDir string) baseSubsystems {
	wtMode := cfg.Execution.WorktreeMode
	if wtMode == "" {
		wtMode = internal.WorktreeMultiAgent
	}

	worktreeMgr := internal.NewWorktreeManager(projectDir, wtMode)
	ptyMgr := internal.NewPTYManager()
	memStore := memory.NewMemoryStore(projectDir, cfg.ConfigDir)
	trustMgr := trust.NewManager(filepath.Join(cfg.ConfigDir, "trust.json"))
	providerMgr := provider.NewProviderManager(cfg)
	modelValidator := provider.NewModelValidator(providerMgr, cfg)
	bgMgr := createBackgroundManager(projectDir)

	sandboxMgr, err := sandbox.NewManager("", worktreeMgr)
	if err != nil {
		log.Printf("Warning: failed to create sandbox manager: %v\n", err)
		sandboxMgr = nil
	}

	toolRegistry := tools.NewToolRegistry()
	registerTools(toolRegistry, worktreeMgr, sandboxMgr, projectDir, cfg, bgMgr)
	if cfg.Tools.Enabled.PTYExec {
		toolRegistry.Register(&tools.PTYExecTool{Mgr: ptyMgr})
	}

	return baseSubsystems{
		worktreeMgr:       worktreeMgr,
		ptyMgr:            ptyMgr,
		memStore:          memStore,
		providerMgr:       providerMgr,
		modelValidator:    modelValidator,
		toolRegistry:      toolRegistry,
		trustMgr:          trustMgr,
		lifecycleRegistry: plugins.NewLifecycleRegistry(),
		bgMgr:             bgMgr,
	}
}

type baseSubsystems struct {
	worktreeMgr       *internal.WorktreeManager
	ptyMgr            *internal.PTYManager
	memStore          *memory.MemoryStore
	providerMgr       *provider.ProviderManager
	modelValidator    *provider.ModelValidator
	toolRegistry      *tools.ToolRegistry
	trustMgr          *trust.Manager
	lifecycleRegistry *plugins.LifecycleRegistry
	bgMgr             *background.Manager
}

func createBackgroundManager(projectDir string) *background.Manager {
	path := filepath.Join(projectDir, ".goa", "bgexec.json")
	mgr, err := background.NewManager(path)
	if err != nil {
		log.Printf("Warning: failed to create durable background manager at %s: %v\n", path, err)
		mgr, _ = background.NewManager("")
	}
	return mgr
}

func initAgentBundle(cfg *config.Config, projectDir string) agentBundle {
	sessionStore := core.NewSessionStore(filepath.Join(projectDir, ".goa"))
	loopDetector := core.NewLoopDetector(core.DefaultLoopDetectorConfig())
	eventBus := event.MakeBus(1024, 32, 32, 32)

	stateStore := core.NewStateStore(projectDir)
	snap, _ := stateStore.Load()
	initialMode := cfg.DefaultModeState()
	if snap.ModeState.Major != "" {
		initialMode = snap.ModeState
	}

	sessionState := core.NewSessionState(initialMode)
	agentMgr := core.NewAgentManager(cfg, sessionStore, loopDetector, sessionState, eventBus, projectDir)
	agentMgr.SetStateStore(stateStore)
	agentLogger := initAgentLogger(cfg, projectDir, agentMgr)
	execCtrl := core.NewExecutionController(cfg, sessionState)

	return agentBundle{
		sessionStore:  sessionStore,
		stateStore:    stateStore,
		stateSnapshot: snap,
		agentMgr:      agentMgr,
		execCtrl:      execCtrl,
		eventBus:      eventBus,
		agentLogger:   agentLogger,
	}
}

type agentBundle struct {
	sessionStore  *core.SessionStore
	stateStore    *core.StateStore
	stateSnapshot core.SessionStateSnapshot
	agentMgr      *core.AgentManager
	execCtrl      *core.ExecutionController
	eventBus      *event.Bus
	agentLogger   *agentic.Logger
}

func initAgentLogger(cfg *config.Config, projectDir string, agentMgr *core.AgentManager) *agentic.Logger {
	if logger := buildAgentLogger(cfg, projectDir); logger != nil {
		agentMgr.SetLogger(logger)
		return logger
	}
	nullLogger := agentic.NewLogger(agentic.Error)
	agentMgr.SetLogger(nullLogger)
	return nullLogger
}

func initHookEngine(cfg *config.Config, projectDir string, agentMgr *core.AgentManager) {
	hookCfg, err := hooks.LoadConfig(cfg.ConfigDir, projectDir)
	if err != nil {
		log.Printf("Warning: failed to load hooks config: %v\n", err)
		return
	}
	if hookCfg == nil || len(hookCfg.Hooks) == 0 {
		return
	}
	hookStore := hooks.NewStore(filepath.Join(projectDir, ".goa", "hooks.log"))
	agentMgr.SetHookEngine(hooks.NewEngine(hookCfg, hookStore))
}

func newAutonomySwitcher(agentMgr *core.AgentManager, cfg *config.Config, setJail func(bool)) commands.AutonomySwitcher {
	return &autonomySwitcher{agentMgr: agentMgr, cfg: cfg, setJail: setJail}
}

// makeJailSetter returns a function that updates the registered bash tool's
// jail flag. SOLO mode enables the jail; other autonomy levels disable it.
func makeJailSetter(toolRegistry *tools.ToolRegistry) func(bool) {
	return func(jail bool) {
		t, ok := toolRegistry.Get("bash")
		if !ok {
			return
		}
		if bt, ok := t.(*tools.BashTool); ok {
			bt.Jail = jail
		}
	}
}

type autonomySwitcher struct {
	agentMgr *core.AgentManager
	cfg      *config.Config
	setJail  func(bool)
}

func (s *autonomySwitcher) Current() internal.AutonomyLevel {
	if s.agentMgr == nil {
		return internal.AutonomyConfirm
	}
	return s.agentMgr.CurrentMode().Autonomy
}

func (s *autonomySwitcher) SetAutonomy(level internal.AutonomyLevel) error {
	if s.agentMgr == nil {
		return nil
	}
	cur := s.agentMgr.CurrentMode()
	s.agentMgr.SetMode(cur.WithAutonomy(level))
	if s.setJail != nil {
		s.setJail(level == internal.AutonomySolo)
	}
	if s.cfg != nil && len(s.cfg.ConfigDir) > 0 {
		// Best-effort persistence is handled by the mode manager.
	}
	return nil
}

func initGoalSystem(projectDir string, eventBus *event.Bus, agentMgr *core.AgentManager, swarmState *swarm.State) (*core.GoalManager, *core.GoalDriver) {
	reminderFn := func(text string) {
		if agentMgr != nil {
			_ = agentMgr.InjectSystemMessage(text)
		}
	}
	publisher := &goalEventPublisher{bus: eventBus}
	manager := core.NewGoalManager(projectDir, core.GoalDependencies{
		Publisher:  publisher,
		Telemetry:  nil,
		ReminderFn: reminderFn,
	})
	// Chain goal + swarm reminders into a single provider so the swarm
	// enter-reminder is prepended to the system prompt every turn while swarm
	// mode is active under a manual/task trigger.
	agentMgr.SetGoalStateProvider(&core.ReminderProvider{
		Sources: []core.GoalReminderSource{
			&core.GoalInjector{Mode: manager.Mode},
			core.SwarmReminder{State: swarmState},
		},
	})
	driver := &core.GoalDriver{
		Mode:  manager.Mode,
		Agent: &agentManagerRunner{agentMgr: agentMgr},
	}
	// Wire goal token tracking: each token stats event reports cumulative
	// tokens for the current turn; compute the delta and accrue to goal.
	// Token totals are per-turn (reset between turns), so a smaller total
	// signals a new turn — reset the accumulator.
	var lastGoalTokens int
	agentMgr.SetGoalTokenRecorder(func(total int) {
		if total < lastGoalTokens {
			lastGoalTokens = 0 // new turn started
		}
		if total > lastGoalTokens {
			delta := total - lastGoalTokens
			if _, err := manager.Mode.RecordTokenUsage(delta); err != nil {
				agentMgr.EmitEvent("Failed to record goal tokens: " + err.Error())
			}
			lastGoalTokens = total
		}
	})
	agentMgr.SetPostTurnHook(func() {
		if active := manager.Mode.GetActiveGoal(); active != nil {
			_ = driver.Drive(context.Background())
		}
	})
	// End-of-turn swarm auto-exit (kimi-code parity): task/tool triggers
	// deactivate after the turn; the manual toggle persists. On auto-exit,
	// inject the exit reminder so the model drops the swarm workflow.
	agentMgr.SetPostTurnHook(func() {
		if swarmState.MaybeAutoExit() {
			_ = agentMgr.InjectSystemMessage(swarm.ExitReminder())
		}
	})
	return manager, driver
}

// wireDreamScheduler registers a post-turn hook that records completed
// sessions for automatic memory consolidation. The scheduler itself is
// started during subsystem assembly once the event bus is available.
func wireDreamScheduler(agentMgr *core.AgentManager, scheduler *dreamScheduler) {
	if scheduler == nil {
		return
	}
	agentMgr.SetPostTurnHook(func() {
		scheduler.RecordSession()
	})
}

type goalEventPublisher struct {
	bus *event.Bus
}

func (p *goalEventPublisher) Publish(snapshot *goal.GoalSnapshot, change *goal.GoalChange) {
	if p.bus == nil {
		return
	}
	select {
	case p.bus.Agent <- event.AgentEvent{GoalUpdate: &event.GoalUpdate{Snapshot: snapshot, Change: change}}:
	default:
	}
}

// agentManagerRunner adapts AgentManager to the core.AgentRunner interface
// used by GoalDriver. It runs turns against the currently active agent.
type agentManagerRunner struct {
	agentMgr *core.AgentManager
}

func (r *agentManagerRunner) Run(ctx context.Context, input string) error {
	agent := r.agentMgr.CurrentAgent()
	if agent == nil {
		return fmt.Errorf("no active agent session")
	}
	return agent.Run(ctx, input)
}

func registerGoalTools(toolRegistry *tools.ToolRegistry, manager *core.GoalManager) {
	reminderFn := func(text string) {
		// Reminders are injected by the agent loop via GoalStateProvider.
	}
	for _, t := range tools.NewGoalTools(manager.Mode, reminderFn) {
		toolRegistry.Register(t)
	}
}

func initSkillAndCommandLayer(cfg *config.Config, projectDir string, toolRegistry *tools.ToolRegistry, goalManager *core.GoalManager, goalDriver *core.GoalDriver, agentMgr *core.AgentManager, trustMgr *trust.Manager, telemetryEnabled bool, swarmState *swarm.State, registry *core.CommandRegistry) skillCommandBundle {
	cfgDir := cfg.ConfigDir
	if cfgDir == "" {
		cfgDir = filepath.Join(projectDir, ".goa")
	}
	pluginRoot := filepath.Join(cfgDir, "plugins")
	pluginMgr, err := plugins.NewManager(pluginRoot, trustMgr)
	if err != nil {
		log.Printf("Warning: failed to create plugin manager: %v\n", err)
	}

	skillDirs := append(config.DefaultSkillDirs(projectDir), cfg.Skills.Dirs...)
	if pluginMgr != nil {
		skillDirs = append(skillDirs, pluginMgr.EnabledSkillDirs()...)
	}
	skillRegistry := skills.NewSkillRegistry(skillDirs)
	skillRegistry.SetEmbeddedFS(skills.EmbeddedSkillsFS)
	skillRegistry.SetTrustChecker(newSkillTrustChecker(trustMgr))
	if err := skillRegistry.LoadAll(); err != nil {
		log.Printf("Warning: failed to load skills: %v\n", err)
	} else if len(skillRegistry.List()) > 0 {
		log.Printf("Loaded %d skills from %d directories\n", len(skillRegistry.List()), len(skillDirs))
	}

	goalCmd := &commands.GoalCommand{
		Mode:             goalManager.Mode,
		Queue:            goalManager.Queue,
		Driver:           goalDriver,
		AutonomySwitcher: newAutonomySwitcher(agentMgr, cfg, makeJailSetter(toolRegistry)),
	}
	// Wire the queue as the goal name pool so newly created active goals pick
	// a friendly alias that does not collide with queued goals.
	goalManager.Mode.SetNamePool(goalManager.Queue)
	authStore := auth.NewStore(filepath.Join(cfgDir, "auth.json"))
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

	// Register the goa_command tool now that the command router exists.
	// The execution context is wired later in assembleSubsystems once the
	// subsystems are fully assembled.
	goaTool := core.NewGoaCommandToolWithContextFn(cmdRouter, func() core.Context { return core.Context{} })

	toolRegistry.Register(goaTool)

	return skillCommandBundle{
		skillRegistry: skillRegistry,
		docEngine:     docEngine,
		cmdRouter:     cmdRouter,
		goaTool:       goaTool,
		pluginMgr:     pluginMgr,
	}
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
}

func initPromptAndWorkflowLayer(cfg *config.Config, projectDir string) (*prompts.Registry, *multiagent.WorkflowRegistry) {
	promptDir := cfg.Prompts.Dir
	if promptDir == "" {
		promptDir = filepath.Join(projectDir, ".goa", "prompts")
	}
	promptReg := prompts.NewRegistry(prompts.EmbeddedFS(), promptDir, filepath.Join(cfg.ConfigDir, "prompts"))

	workflowReg := multiagent.NewWorkflowRegistry(promptReg)
	if err := workflowReg.LoadWorkflowTree(filepath.Join(projectDir, "workflows")); err != nil {
		log.Printf("Warning: failed to load project workflows: %v\n", err)
	}
	if err := workflowReg.LoadDir(filepath.Join(projectDir, ".goa", "workflows")); err != nil {
		log.Printf("Warning: failed to load .goa/workflows: %v\n", err)
	}
	if err := workflowReg.LoadWorkflowTree(filepath.Join(cfg.ConfigDir, "workflows")); err != nil {
		log.Printf("Warning: failed to load user workflows: %v\n", err)
	}

	return promptReg, workflowReg
}

func registerAgentDrivenTools(toolRegistry *tools.ToolRegistry, tools []agentic.Tool, cfg *config.Config) (*multiagent.RequestReviewTool, *multiagent.DelegateTool) {
	var requestReviewTool *multiagent.RequestReviewTool
	var delegateTool *multiagent.DelegateTool
	for _, t := range tools {
		switch v := t.(type) {
		case *multiagent.RequestReviewTool:
			if !cfg.Tools.Enabled.RequestReview {
				continue
			}
			requestReviewTool = v
		case *multiagent.DelegateTool:
			if !cfg.Tools.Enabled.DelegateTo {
				continue
			}
			delegateTool = v
		}
		toolRegistry.Register(t)
	}
	return requestReviewTool, delegateTool
}

func createAgentPool(mdl agenticprovider.Model, providerMgr *provider.ProviderManager, toolRegistry *tools.ToolRegistry, promptReg *prompts.Registry, cfg *config.Config, modeRegistry *core.ModeRegistry, swarmState *swarm.State, taskBus *tasks.Bus, agentMgr *core.AgentManager) *multiagent.AgentPool {
	allTools := toolRegistry.All()
	streamOpts := providerMgr.BuildStreamOptions()
	pool := multiagent.NewAgentPool(mdl, streamOpts, allTools)
	pool.PromptRegistry = promptReg
	pool.SetGoaConfig(cfg)

	pool.ModelFactory = func(modelName string) (agenticprovider.Model, error) {
		return providerMgr.ResolveModelByID(modelName)
	}
	pool.ProviderModelFactory = func(providerID, modelName string) (agenticprovider.Model, error) {
		return providerMgr.ResolveModelForProvider(providerID, modelName)
	}

	configureRoleModels(pool, cfg)
	// Register AgentTool and AgentSwarmTool with ModeResolver so sub-agents
	// get mode-appropriate prompts, tools, and temperature settings.
	registerSubAgentTools(toolRegistry, pool, modeRegistry, swarmState, taskBus, agentMgr)
	return pool
}

func configureRoleModels(pool *multiagent.AgentPool, cfg *config.Config) {
	if cfg.MultiAgent.CompanionModel != "" {
		pool.SetConfig("companion", multiagent.AgentConfig{
			ModelName:       cfg.MultiAgent.CompanionModel,
			ProviderID:      cfg.MultiAgent.CompanionProvider,
			ReasoningEffort: agentic.ReasoningEffort(cfg.GetThinkingLevel("companion")),
		})
	}
	if cfg.MultiAgent.PlannerModel != "" {
		pool.SetConfig("planner", multiagent.AgentConfig{
			ModelName:       cfg.MultiAgent.PlannerModel,
			ReasoningEffort: agentic.ReasoningEffort(cfg.GetThinkingLevel("planner")),
		})
	}
	if cfg.MultiAgent.CoderModel != "" {
		pool.SetConfig("coder", multiagent.AgentConfig{
			ModelName:       cfg.MultiAgent.CoderModel,
			ReasoningEffort: agentic.ReasoningEffort(cfg.GetThinkingLevel("coder")),
		})
	}
}

func wireForegroundOrchestrator(pool *multiagent.AgentPool, promptReg *prompts.Registry, agentMgr *core.AgentManager, cfg *config.Config, workflowReg *multiagent.WorkflowRegistry) *multiagent.ForegroundOrchestrator {
	orch := multiagent.NewForegroundOrchestrator(pool)
	pool.SetOrchestrator(orch)
	orch.SetPromptRegistry(promptReg)
	orch.SetSteeringQueue(agentMgr.SteeringQueue())
	orch.ModeSwitchCallback = makeModeSwitchCallback(agentMgr)
	agentMgr.SetForegroundOrchestrator(orch)
	return orch
}

func makeModeSwitchCallback(agentMgr *core.AgentManager) func(string) {
	return func(agentName string) {
		var major internal.MajorMode
		switch agentName {
		case role.Planner:
			major = internal.MajorPlanner
		case role.Coder:
			major = internal.MajorCoder
		case role.Reviewer, role.Companion:
			major = internal.MajorReviewer
		}
		if major != "" {
			cur := agentMgr.CurrentMode()
			agentMgr.SetMode(cur.WithMajor(major))
		}
	}
}

func wireCompanionCreation(pool *multiagent.AgentPool, agentMgr *core.AgentManager, snap core.SessionStateSnapshot) {
	origOnCreated := pool.OnAgentCreated
	pool.OnAgentCreated = func(role string, agent *agentic.Agent) {
		if origOnCreated != nil {
			origOnCreated(role, agent)
		}
		if role != "companion" {
			return
		}
		agentMgr.SetCompanionAgent(agent)
		restoreCompanionHistory(agent, snap.CompanionHistory)
	}
}

func restoreCompanionHistory(agent *agentic.Agent, rawHistory []json.RawMessage) {
	if len(rawHistory) == 0 {
		return
	}
	var history []agentic.Message
	for _, raw := range rawHistory {
		var msg agentic.Message
		if err := json.Unmarshal(raw, &msg); err == nil {
			history = append(history, msg)
		}
	}
	if len(history) > 0 {
		agent.SetHistory(history)
	}
}

func registerSkillRunnerIfNeeded(toolRegistry *tools.ToolRegistry, skillRegistry *skills.SkillRegistry, pool *multiagent.AgentPool, promptReg *prompts.Registry, cfg *config.Config) {
	if cfg.Skills.ExecutionMode != config.AgenticSkillModeSubAgent {
		return
	}
	skillRunner := skills.NewSkillRunnerTool(skillRegistry, pool, promptReg)
	toolRegistry.Register(skillRunner)
}

func registerBuiltinWorkflows(workflowReg *multiagent.WorkflowRegistry) {
	for _, w := range multiagent.BuiltinPipelines() {
		workflowReg.Register(w)
	}
}

func restoreSessionState(agentMgr *core.AgentManager, snap core.SessionStateSnapshot, requestReviewTool *multiagent.RequestReviewTool, delegateTool *multiagent.DelegateTool, cfg *config.Config) {
	agentMgr.SetAgentDrivenChangeCallback(func(enabled bool) {
		if requestReviewTool != nil {
			requestReviewTool.Enabled = enabled
		}
		if delegateTool != nil {
			delegateTool.Enabled = enabled
		}
	})

	if snap.MinorMode == "companion" || snap.AgentDrivenEnabled {
		if err := agentMgr.SetMinorMode("companion", true); err != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to restore minor mode: %v\n", err)
		}
	}

	level := string(cfg.GetThinkingLevel("main_agent"))
	if snap.ThinkingLevel != "" {
		level = snap.ThinkingLevel
	}
	if err := agentMgr.SetThinkingLevel(level); err != nil {
		fmt.Fprintf(os.Stderr, "Warning: failed to restore thinking level: %v\n", err)
	}
}

func wireAgentBus(agentMgr *core.AgentManager, pool *multiagent.AgentPool, orch *multiagent.ForegroundOrchestrator, maxCompanionCycles int) {
	agentBus := agentMgr.AgentBus()
	pool.SetAgentBus(agentBus)
	orch.SetAgentBus(agentBus)
	orch.SetCompanionMaxMessages(maxCompanionCycles)
}

func attachAgentDrivenToolPools(tools []agentic.Tool, pool *multiagent.AgentPool) {
	for _, t := range tools {
		switch v := t.(type) {
		case *multiagent.RequestReviewTool:
			v.Pool = pool
		case *multiagent.DelegateTool:
			v.Pool = pool
		}
	}
}

func assembleSubsystems(cfg *config.Config, loader *config.CascadeLoader, projectDir string, base baseSubsystems, ab agentBundle, sc skillCommandBundle, promptReg *prompts.Registry, workflowReg *multiagent.WorkflowRegistry, agentPool *multiagent.AgentPool, foregroundOrch *multiagent.ForegroundOrchestrator, requestReviewTool *multiagent.RequestReviewTool, delegateTool *multiagent.DelegateTool, pipelineRunner *multiagent.PipelineRunner, goalManager *core.GoalManager, goalDriver *core.GoalDriver, opts RuntimeOptions, registry *core.CommandRegistry) *subsystems {
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
		projectDir:        projectDir,
		trustMgr:          base.trustMgr,
		commandStats:      NewCommandStats(projectDir),
		stateStore:        ab.stateStore,
		goalManager:       goalManager,
		goalDriver:        goalDriver,
		orchAdapter:       NewOrchestratorAdapter(agentPool, cfg),
		orchActive:        orchestrator.NewActiveRuntime(),
		contextFiles:      internal.LoadProjectContextFiles(projectDir, cfg.ConfigDir),
		requestReviewTool: requestReviewTool,
		delegateTool:      delegateTool,
		logger:            ab.agentLogger,
		lifecycleRegistry: base.lifecycleRegistry,
		pluginMgr:         sc.pluginMgr,
		MemoryEnabled:     !opts.NoMemory,
		MemoryBudget:      opts.MemoryBudget,
		perfLoad:          opts.PerfLoad,
		perfLoadDuration:  opts.PerfLoadDuration,
		registry:          registry,
		bgMgr:             base.bgMgr,
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
	_ = s.registry.Register(orchCmd)

	s.dreamScheduler = newDreamScheduler(s)
	_ = s.dreamScheduler.readSchedulerState()
	s.dreamScheduler.Start()
	wireDreamScheduler(s.agentMgr, s.dreamScheduler)

	if s.modelValidator != nil {
		s.modelValidator.Start(context.Background(), 5*time.Minute)
	}

	loadEnabledPlugins(s)

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
// needed to spawn sub-agents with mode-appropriate configuration.
func registerSubAgentTools(reg *tools.ToolRegistry, pool *multiagent.AgentPool, modeRegistry *core.ModeRegistry, swarmState *swarm.State, taskBus *tasks.Bus, agentMgr *core.AgentManager) {
	resolver := &modeResolverAdapter{reg: modeRegistry}
	currentMode := func() internal.ModeState {
		if agentMgr == nil {
			return internal.ModeState{}
		}
		return agentMgr.CurrentMode()
	}

	agentTool := &multiagent.AgentTool{
		Pool:         pool,
		ModeResolver: resolver,
		TaskBus:      taskBus,
		CurrentMode:  currentMode,
	}
	reg.Register(agentTool)

	swarmTool := &toolsSwarm.AgentSwarmTool{
		Pool:         pool,
		ModeResolver: resolver,
		TaskBus:      taskBus,
		SwarmState:   swarmState,
		CurrentMode:  currentMode,
	}
	reg.Register(swarmTool)
}
