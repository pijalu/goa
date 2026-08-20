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
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/background"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/internal/hooks"
	"github.com/pijalu/goa/internal/lsp"
	"github.com/pijalu/goa/internal/mcp"
	"github.com/pijalu/goa/internal/sandbox"
	"github.com/pijalu/goa/internal/trust"
	"github.com/pijalu/goa/memory"
	"github.com/pijalu/goa/plugins"
	"github.com/pijalu/goa/provider"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tools/common"
)

func initBaseSubsystems(cfg *config.Config, projectDir string, headless bool) baseSubsystems {
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
	lspMgr, mcpMgr := registerTools(toolRegistry, worktreeMgr, sandboxMgr, projectDir, cfg, bgMgr, headless)
	if cfg.Tools.Enabled.Terminals {
		toolRegistry.Register(&tools.TerminalsTool{
			Mgr:        ptyMgr,
			Blocked:    cfg.Tools.Terminal.Sandbox.BlockedCommands,
			Allowed:    cfg.Tools.Terminal.Sandbox.AllowedCommands,
			Bypass:     !cfg.Tools.Terminal.Sandbox.Enabled,
			ProjectDir: projectDir,
		})
	}

	// Scheduler tools (P18/TL2): persistent job store + model-facing
	// schedule_create/delete/list. Always registered — they are harmless
	// read/write tools over a durable file, matching the dsh schedule package
	// which exposes them for every session. Their schemas are deferred to
	// tool_search (tools/deferred.go) since they are rarely used; the model
	// loads them on demand, after which they execute normally.
	scheduleStore := newScheduleStore(scheduleStorePath(projectDir))
	toolRegistry.Register(&tools.ScheduleCreateTool{Store: scheduleStore})
	toolRegistry.Register(&tools.ScheduleDeleteTool{Store: scheduleStore})
	toolRegistry.Register(&tools.ScheduleListTool{Store: scheduleStore})

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
		lspMgr:            lspMgr,
		mcpManager:        mcpMgr,
		scheduleStore:     scheduleStore,
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
	lspMgr            *lsp.Manager
	mcpManager        *mcp.Manager
	scheduleStore     tools.ScheduleStore
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

// loopDetectorConfigFrom derives the runtime loop-detector configuration from
// the persisted user/project config, honouring the persistent disable switches
// (execution.disable_thinking_loop_detection / disable_tool_loop_detection).
func loopDetectorConfigFrom(cfg *config.Config) core.LoopDetectorConfig {
	ldCfg := core.DefaultLoopDetectorConfig()
	if cfg == nil {
		return ldCfg
	}
	// The persisted loop thresholds drive the live tool-loop detector.
	// Non-positive values keep the built-in default: a zero threshold would
	// trip on the first recorded call, so it is never a valid threshold.
	if cfg.Execution.LoopWarning > 0 {
		ldCfg.LoopWarning = cfg.Execution.LoopWarning
	}
	if cfg.Execution.LoopInterrupt > 0 {
		ldCfg.LoopInterrupt = cfg.Execution.LoopInterrupt
	}
	if cfg.Execution.DisableThinkingLoopDetection != nil && *cfg.Execution.DisableThinkingLoopDetection {
		ldCfg.ThinkingDisabled = true
	}
	if cfg.Execution.DisableToolLoopDetection != nil && *cfg.Execution.DisableToolLoopDetection {
		ldCfg.ToolDisabled = true
	}
	if cfg.Execution.DisableStreamLoopDetection != nil && *cfg.Execution.DisableStreamLoopDetection {
		ldCfg.StreamDisabled = true
	}
	if cfg.Execution.DisableThinkingStallDetection != nil && *cfg.Execution.DisableThinkingStallDetection {
		ldCfg.StallDisabled = true
	}
	ldCfg.MaxStreamRepeats = cfg.Execution.StreamLoopMaxRepeats
	ldCfg.MinStreamPeriod = cfg.Execution.StreamLoopMinPeriod
	return ldCfg
}

func initAgentBundle(cfg *config.Config, projectDir string) agentBundle {
	sessionStore := core.NewSessionStore(filepath.Join(projectDir, ".goa"))
	loopDetector := core.NewLoopDetector(loopDetectorConfigFrom(cfg))
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
	// Tool-result spill policy (gap CX2): when tools.max_inline_bytes is set,
	// oversized plain-text tool results spill verbatim into the session-scoped
	// dir ~/.goa/spill/<session>/ and the model sees a bounded preview+notice.
	if cfg.Tools.MaxInlineBytes > 0 {
		maxInline := cfg.Tools.MaxInlineBytes
		agentMgr.SetSpillPolicyFactory(func(sessionID string) agentic.SpillPolicy {
			if sessionID == "" {
				return nil // no session owner: keep results inline
			}
			dir := common.SessionSpillDir(internal.GoaHomeDir(), sessionID)
			return &tools.SpillPolicy{
				MaxInlineBytes: maxInline,
				Store:          common.NewSpillStore(dir),
			}
		})
	}
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
	sessionStore   *core.SessionStore
	stateStore     *core.StateStore
	stateSnapshot  core.SessionStateSnapshot
	agentMgr       *core.AgentManager
	execCtrl       *core.ExecutionController
	eventBus       *event.Bus
	agentLogger    *agentic.Logger
	stickyProvider *stickySkillProvider
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
	if hookCfg == nil {
		return
	}
	for _, w := range hookCfg.Warnings {
		log.Printf("Warning: hooks config: %s\n", w)
	}
	if len(hookCfg.Hooks) == 0 {
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

// configureGoalMode applies the goals.* config keys to the goal mode: done
// gate, machine verification, escalation bound, default turn budget, and the
// independent judge. Extracted from initGoalSystem to keep both within the
// complexity budget.
