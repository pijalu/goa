// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/background"
	"github.com/pijalu/goa/internal/spinner"
	"github.com/pijalu/goa/tui"
	bgpanel "github.com/pijalu/goa/tui/background"
	goaltui "github.com/pijalu/goa/tui/goal"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
)

func (a *App) buildTUI() (*tui.TUI, *tui.ChatViewport, *tui.Editor) {
	subs := a.subs

	engine, chat, agentContent, agentTabBar, statusBar, goalBubble, inp, statusFooter, bgPanel := a.createTUIComponents()
	subs.goalBubble = goalBubble
	a.configureKeyLogging(engine)
	a.attachInputHandlers(inp, engine)
	a.assembleEngine(engine, headerFrom(subs.projectDir), chat, agentContent, agentTabBar, statusBar, goalBubble, bgPanel, inp, statusFooter)
	a.configureInputEditor(inp, engine)
	a.loadInputHistory(inp)
	a.applyThinkingLevelToUI(mainThinkingLevel(subs))

	if err := engine.Start(); err != nil {
		// TUI startup failure is fatal.
		os.Exit(1)
	}

	a.finalizeTUI(engine, chat, agentContent, agentTabBar, statusFooter, statusBar, bgPanel, inp)
	return engine, chat, inp
}

func headerFrom(projectDir string) *tui.Header {
	h := tui.NewHeader("goa", internal.Version)
	return h
}

func (a *App) createTUIComponents() (*tui.TUI, *tui.ChatViewport, *orchpanel.AgentContent, *orchpanel.AgentTabBar, *tui.StatusMsg, *goaltui.Bubble, *tui.Editor, *tui.Footer, *bgpanel.Panel) {
	projectDir := a.subs.projectDir
	var ft tui.Terminal = tui.NewProcessTerminal()
	logPath := a.subs.cfg.Logging.TerminalLog
	if logPath == "" {
		logPath = os.Getenv("GOA_DEBUG_TERMINAL")
	}
	if logPath != "" {
		if lt, err := tui.NewLogTerminal(ft, logPath); err == nil {
			ft = lt
			if a.subs.logger != nil {
				a.subs.logger.Log(agentic.Info, "terminal debug log enabled: %s", logPath)
			}
		} else if a.subs.logger != nil {
			a.subs.logger.Log(agentic.Error, "failed to enable terminal debug log %s: %v", logPath, err)
		}
	}
	engine := tui.NewTUI(ft)
	if rt := a.renderTracePath(); rt != "" {
		if err := engine.SetRenderTrace(rt); err == nil {
			if a.subs.logger != nil {
				a.subs.logger.Log(agentic.Info, "render trace enabled: %s", rt)
			}
		} else if a.subs.logger != nil {
			a.subs.logger.Log(agentic.Error, "failed to enable render trace %s: %v", rt, err)
		}
	}
	chat := tui.NewChatViewport()
	agentContent := orchpanel.NewAgentContent()
	agentTabBar := orchpanel.NewAgentTabBar()
	statusBar := tui.NewStatusMsg()
	inp := tui.NewEditor()
	goalBubble := goaltui.NewBubble()
	statusFooter := tui.NewFooter()
	statusFooter.SetData(tui.FooterData{Workdir: projectDir})
	statusFooter.RefreshGit()
	bgPanel := bgpanel.NewPanel(nil)
	return engine, chat, agentContent, agentTabBar, statusBar, goalBubble, inp, statusFooter, bgPanel
}

func (a *App) configureKeyLogging(engine *tui.TUI) {
	cfg := a.subs.cfg
	if !cfg.Logging.TraceKeys {
		return
	}
	logPath := a.keyLogPath()
	if err := engine.SetKeyLog(logPath); err != nil {
		a.subs.logger.Log(agentic.Error, "failed to enable key trace log: %v", err)
	}
}

func (a *App) keyLogPath() string {
	cfg := a.subs.cfg
	if cfg.Logging.File != "" {
		return cfg.Logging.File
	}
	if cd, err := os.UserCacheDir(); err == nil {
		return filepath.Join(cd, "goa", "keys.log")
	}
	return filepath.Join(a.subs.projectDir, ".goa", "keys.log")
}

// renderTracePath resolves the per-frame compositor trace destination from
// config, falling back to the GOA_DEBUG_RENDER convenience env var. Mirrors
// the terminal-log resolution above.
func (a *App) renderTracePath() string {
	if a.subs.cfg.Logging.RenderTrace != "" {
		return a.subs.cfg.Logging.RenderTrace
	}
	return os.Getenv("GOA_DEBUG_RENDER")
}

func (a *App) attachInputHandlers(inp *tui.Editor, engine *tui.TUI) {
	subs := a.subs
	inp.OnEscape = func() { a.handleEscape() }
	engine.OnDeleteLast = func() {
		if subs.chat != nil && subs.tuiEngine != nil {
			subs.chat.RemoveLastMessage()
			subs.tuiEngine.RequestRender()
		}
	}
	engine.OnToggleGoalBubble = func() {
		if subs.goalBubble != nil {
			subs.goalBubble.ToggleCollapse()
		}
	}
	engine.OnCycleThinkingLevel = func() { a.handleCycleThinkingLevel() }
	engine.OnChangeMode = func() { a.handleChangeMode() }
	engine.OnOpenModeSelector = func() { a.handleOpenModeSelector() }
	engine.OnCycleAutonomy = func() { a.handleCycleAutonomy() }
	engine.OnChangeModel = func() { a.handleChangeModel() }
	engine.OnToggleThinkingBlocks = func() { a.handleToggleThinkingBlocks() }
	engine.OnOpenAgentTabs = func() { a.openAgentTabSelector() }
	a.wireOrchCommandCallbacks()
	engine.OnCancelInputRequest = func() bool { return a.cancelPendingMainInput() }
}

func (a *App) handleEscape() {
	subs := a.subs
	if subs.agentMgr != nil {
		subs.agentMgr.Interrupt()
	}
	if subs.ptyMgr != nil {
		subs.ptyMgr.Cleanup()
	}
	if subs.toolRegistry != nil {
		a.stopBackgroundProcesses()
	}
}

func (a *App) stopBackgroundProcesses() {
	bg, ok := a.subs.toolRegistry.Get("bg_exec")
	if ok {
		if stopper, ok := bg.(interface{ StopAll() }); ok {
			stopper.StopAll()
		}
	}
	// Shut down gopls so it does not leak across restarts/reloads.
	if a.subs.lspMgr != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = a.subs.lspMgr.Close(ctx)
		cancel()
	}
}

func (a *App) assembleEngine(engine *tui.TUI, header *tui.Header, chat *tui.ChatViewport, agentContent *orchpanel.AgentContent, agentTabBar *orchpanel.AgentTabBar, statusBar *tui.StatusMsg, goalBubble *goaltui.Bubble, bgPanel *bgpanel.Panel, inp *tui.Editor, footer *tui.Footer) {
	_ = agentContent
	_ = agentTabBar
	engine.AddChild(header)
	engine.AddChild(chat)
	engine.AddChild(statusBar)
	engine.AddChild(goalBubble)
	engine.AddChild(bgPanel)
	engine.AddChild(inp)
	engine.AddChild(footer)
	engine.SetFocus(inp)
}

func (a *App) configureInputEditor(inp *tui.Editor, engine *tui.TUI) {
	subs := a.subs
	cmdCompleter := a.buildCommandCompleter()
	fileComp := tui.NewFileCompleter(subs.projectDir)
	inp.SetCompleter(tui.NewCombinedCompleter(cmdCompleter, fileComp))
	subs.inputEditor = inp
	inp.SetMaxLines(3)
	inp.SetTUI(engine)
}

func (a *App) finalizeTUI(engine *tui.TUI, chat *tui.ChatViewport, agentContent *orchpanel.AgentContent, agentTabBar *orchpanel.AgentTabBar, footer *tui.Footer, statusBar *tui.StatusMsg, bgPanel *bgpanel.Panel, inp *tui.Editor) {
	subs := a.subs
	statusBar.SetTUI(engine)
	statusBar.SetOnFrameChange(func() {
		if chat != nil {
			chat.InvalidateRunningToolWidgets()
		}
	})

	if subs.bgMgr != nil && bgPanel != nil {
		bgPanel.SetSnapshot(func() []bgpanel.Task {
			return taskSnapshotsFromManager(subs.bgMgr)
		})
	}

	engine.SetTitle("goa - " + filepath.Base(subs.projectDir))

	subs.chat = chat
	subs.footer = footer
	subs.tuiEngine = engine
	subs.statusMsg = statusBar
	subs.agentContent = agentContent
	subs.agentTabBar = agentTabBar
	subs.bgPanel = bgPanel

	footer.SetData(a.initialFooterData())
	tui.SetToolProjectDir(subs.projectDir)
}

func taskSnapshotsFromManager(mgr *background.Manager) []bgpanel.Task {
	tasks := mgr.List()
	out := make([]bgpanel.Task, len(tasks))
	for i, t := range tasks {
		out[i] = bgpanel.Task{
			ID:      t.ID,
			Command: t.Command,
			Status:  string(t.Status),
			PID:     t.PID,
		}
	}
	return out
}

func (a *App) initialFooterData() tui.FooterData {
	subs := a.subs
	cfg := subs.cfg
	mode := subs.effectiveModeState()
	return tui.FooterData{
		Workdir:                subs.projectDir,
		Profile:                string(mode.Major),
		Mode:                   string(mode.Autonomy),
		Model:                  activeModelDisplay(subs),
		Provider:               cfg.ActiveProvider,
		CompanionModel:         companionModelDisplay(subs),
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	}
}

func (a *App) buildCommandCompleter() *tui.CommandCompleter {
	cmdNames, descriptions := collectCmdNames(a.subs.registry)
	addAliases(cmdNames, descriptions, a.subs.cfg.Aliases)

	cmdCompleter := tui.NewCommandCompleter(cmdNames, descriptions)
	cmdCompleter.SetArgCompleter(a.buildArgCompleter())

	if a.subs.commandStats != nil {
		cmdCompleter.SetFreqOrder(a.subs.commandStats.All())
	}
	cmdCompleter.SetMinThreshold(a.subs.cfg.Completion.MinUsageThreshold)
	cmdCompleter.SetMaxMostUsed(a.subs.cfg.Completion.MaxMostUsed)
	return cmdCompleter
}

// collectCmdNames returns all command names and descriptions from the given registry.
func collectCmdNames(registry *core.CommandRegistry) ([]string, map[string]string) {
	allCmds := registry.All()
	cmdNames := make([]string, 0, len(allCmds))
	descriptions := make(map[string]string, len(allCmds)*2)
	for _, c := range allCmds {
		name := "/" + c.Name()
		cmdNames = append(cmdNames, name)
		descriptions[name] = c.ShortHelp()
		for _, alias := range c.Aliases() {
			a := "/" + alias
			cmdNames = append(cmdNames, a)
			descriptions[a] = c.ShortHelp()
		}
	}
	return cmdNames, descriptions
}

// addAliases adds user-defined aliases to the command name/description maps.
func addAliases(cmdNames []string, descriptions map[string]string, aliases map[string]string) {
	for alias, target := range aliases {
		a := "/" + alias
		cmdNames = append(cmdNames, a)
		descriptions[a] = "alias for /" + target
	}
}

// buildArgCompleter creates the argument completer function.
func (a *App) buildArgCompleter() func(cmdName, argPrefix string) []tui.Completion {
	return func(cmdName, argPrefix string) []tui.Completion {
		name := strings.TrimPrefix(cmdName, "/")
		cmd, found := resolveCommandOrAlias(a.subs.registry, name, a.subs.cfg.Aliases)
		if !found {
			return nil
		}
		ac, ok := cmd.(core.ArgCompleter)
		if !ok {
			return nil
		}
		ctx := core.Context{
			SkillRegistry:    a.subs.skillRegistry,
			ToolRegistry:     a.subs.toolRegistry,
			DocsProvider:     &DocsProvider{},
			MemoryStore:      a.subs.memStore,
			ProviderManager:  a.subs.providerMgr,
			Config:           a.subs.cfg,
			ReloadHandler:    &ReloadHandler{subs: a.subs},
			PTYManager:       a.subs.ptyMgr,
			WorkflowRegistry: a.subs.workflowReg,
		}
		comps := ac.CompleteArgs(ctx, argPrefix)
		result := make([]tui.Completion, len(comps))
		for i, c := range comps {
			result[i] = tui.Completion{Value: c.Value, Description: c.Description}
		}
		return result
	}
}

// resolveCommandOrAlias looks up a command by name, resolving aliases if not found.
func resolveCommandOrAlias(registry *core.CommandRegistry, name string, aliases map[string]string) (core.Command, bool) {
	cmd, found := registry.Resolve(name)
	if found {
		return cmd, true
	}
	if aliases != nil {
		if target, ok := aliases[name]; ok && !strings.Contains(target, ":") {
			return registry.Resolve(target)
		}
	}
	return nil, false
}

// initTheme applies the configured TUI theme.
func initTheme(cfg *config.Config) {
	if cfg.TUI.Theme == "light" {
		tui.TheTheme = tui.LightTheme()
	} else {
		tui.TheTheme = tui.DarkTheme()
	}
	tui.SyncToolTheme()
}

// initSpinner sets the active spinner from config. An empty or unset spinner
// name uses the default "arc" spinner; "none" explicitly disables animation.
func initSpinner(cfg *config.Config) {
	name := cfg.TUI.Spinner
	if name == "none" {
		tui.SetSpinner(spinner.Definition{})
		return
	}
	if name == "" {
		_, def := spinner.Default()
		tui.SetSpinner(def)
		return
	}
	if def, ok := spinner.Get(name); ok {
		tui.SetSpinner(def)
	}
}

// loadInputHistory loads the input history from the state store.
func (a *App) loadInputHistory(inp *tui.Editor) {
	subs := a.subs
	if subs.agentMgr == nil {
		return
	}
	history := subs.agentMgr.GetInputHistory()
	if len(history) > 0 {
		inp.SetHistory(history)
	}
}

// saveInputHistory saves the input history to the state store.
func (a *App) saveInputHistory(inp *tui.Editor) {
	subs := a.subs
	if subs.agentMgr == nil {
		return
	}
	history := inp.GetHistory()
	if err := subs.agentMgr.SetInputHistory(history); err != nil {
		// Log error but don't fail the exit
		subs.logger.Log(agentic.Error, "failed to save input history: %v", err)
	}
}

// applyThinkingLevelToUI propagates the thinking level to components that
// color their separator lines based on it.
func (a *App) applyThinkingLevelToUI(level string) {
	subs := a.subs
	color := tui.ThinkingLevelSeparatorColor(level)
	if subs.goalBubble != nil {
		subs.goalBubble.SetSeparatorColor(color)
	}
	if subs.inputEditor != nil {
		subs.inputEditor.SetThinkingLevel(level)
	}
}
