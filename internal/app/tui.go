// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/spinner"
	"github.com/pijalu/goa/tui"
	goaltui "github.com/pijalu/goa/tui/goal"
)

func (a *App) buildTUI() (*tui.TUI, *tui.ChatViewport, *tui.Editor) {
	subs := a.subs

	engine, chat, pendingMsgs, statusBar, goalBubble, inp, statusFooter := a.createTUIComponents()
	subs.goalBubble = goalBubble
	a.configureKeyLogging(engine)
	a.attachInputHandlers(inp, engine)
	a.assembleEngine(engine, headerFrom(subs.projectDir), chat, pendingMsgs, statusBar, goalBubble, inp, statusFooter)
	a.configureInputEditor(inp, engine)
	a.loadInputHistory(inp)
	a.applyThinkingLevelToUI(mainThinkingLevel(subs))

	if err := engine.Start(); err != nil {
		// TUI startup failure is fatal.
		os.Exit(1)
	}

	a.finalizeTUI(engine, chat, statusFooter, pendingMsgs, statusBar, inp)
	return engine, chat, inp
}

func headerFrom(projectDir string) *tui.Header {
	h := tui.NewHeader("goa", internal.Version)
	return h
}

func (a *App) createTUIComponents() (*tui.TUI, *tui.ChatViewport, *tui.StatusMsg, *tui.StatusMsg, *goaltui.Bubble, *tui.Editor, *tui.Footer) {
	projectDir := a.subs.projectDir
	ft := tui.NewProcessTerminal()
	engine := tui.NewTUI(ft)
	chat := tui.NewChatViewport()
	pendingMsgs := tui.NewStatusMsg()
	statusBar := tui.NewStatusMsg()
	inp := tui.NewEditor()
	goalBubble := goaltui.NewBubble()
	statusFooter := tui.NewFooter()
	statusFooter.SetData(tui.FooterData{Workdir: projectDir})
	statusFooter.RefreshGit()
	return engine, chat, pendingMsgs, statusBar, goalBubble, inp, statusFooter
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
	if !ok {
		return
	}
	if stopper, ok := bg.(interface{ StopAll() }); ok {
		stopper.StopAll()
	}
}

func (a *App) assembleEngine(engine *tui.TUI, header *tui.Header, chat *tui.ChatViewport, pendingMsgs, statusBar *tui.StatusMsg, goalBubble *goaltui.Bubble, inp *tui.Editor, footer *tui.Footer) {
	engine.AddChild(header)
	engine.AddChild(chat)
	engine.AddChild(pendingMsgs)
	engine.AddChild(statusBar)
	engine.AddChild(goalBubble)
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

func (a *App) finalizeTUI(engine *tui.TUI, chat *tui.ChatViewport, footer *tui.Footer, pendingMsgs, statusBar *tui.StatusMsg, inp *tui.Editor) {
	subs := a.subs
	pendingMsgs.SetTUI(engine)
	statusBar.SetTUI(engine)

	engine.SetTitle("goa - " + filepath.Base(subs.projectDir))

	subs.chat = chat
	subs.footer = footer
	subs.tuiEngine = engine
	subs.statusMsg = statusBar
	subs.pendingMsgs = pendingMsgs

	footer.SetData(a.initialFooterData())
}

func (a *App) initialFooterData() tui.FooterData {
	subs := a.subs
	cfg := subs.cfg
	return tui.FooterData{
		Workdir:                subs.projectDir,
		Profile:                cfg.ActiveMajor(),
		Mode:                   string(cfg.DefaultModeState().Autonomy),
		Model:                  activeModelDisplay(subs),
		Provider:               cfg.ActiveProvider,
		CompanionModel:         companionModelDisplay(subs),
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	}
}

func (a *App) buildCommandCompleter() *tui.CommandCompleter {
	cmdNames, descriptions := collectCmdNames()
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

// collectCmdNames returns all command names and descriptions from the global registry.
func collectCmdNames() ([]string, map[string]string) {
	allCmds := core.GlobalRegistry().All()
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
		cmd, found := resolveCommandOrAlias(name, a.subs.cfg.Aliases)
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
func resolveCommandOrAlias(name string, aliases map[string]string) (core.Command, bool) {
	cmd, found := core.GlobalRegistry().Resolve(name)
	if found {
		return cmd, true
	}
	if aliases != nil {
		if target, ok := aliases[name]; ok && !strings.Contains(target, ":") {
			return core.GlobalRegistry().Resolve(target)
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

// initSpinner sets the active spinner from config.
func initSpinner(cfg *config.Config) {
	name := cfg.TUI.Spinner
	if name == "" || name == "none" {
		tui.SetSpinner(spinner.Definition{})
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
