// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"strings"
	"time"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/lsp"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tools/ask"
	"github.com/pijalu/goa/tui"
)

// coreContextForCommand builds the core.Context passed to slash commands.
func coreContextForCommand(subs *subsystems, app *App) core.Context {
	ctx := core.Context{
		Config:                 subs.cfg,
		ProjectDir:             subs.projectDir,
		InitialActiveProvider:  subs.cfg.ActiveProvider,
		InitialActiveModel:     subs.cfg.ActiveModel,
		AgentManager:           subs.agentMgr,
		ExecutionController:    subs.execCtrl,
		ToolRegistry:           toolRegistryFor(subs),
		ToolFactory:            makeToolFactory(subs),
		ToolTeardown:           makeToolTeardown(subs),
		LiveTools:              liveToolSetProvider(subs),
		SkillRegistry:          skillRegistryFor(subs),
		ProviderManager:        subs.providerMgr,
		ModelValidator:         subs.modelValidator,
		MemoryStore:            subs.memStore,
		SessionStore:           subs.sessionStore,
		ConfigSaver:            subs.loader,
		DocsProvider:           &DocsProvider{},
		EventBus:               subs.events,
		WorktreeManager:        subs.worktreeMgr,
		PipelineRunner:         subs.pipelineRunner,
		ModeRegistry:           subs.modeRegistry,
		AssistantText:          lastAssistantText(subs),
		ForegroundOrchestrator: subs.foregroundOrch,
		AgentPool:              subs.agentPool,
		TeamManager:            subs.teamManager,
		SkillSubAgentRunner:    &skillSubAgentRunner{pool: subs.agentPool},
		WorkflowRegistry:       subs.workflowReg,
		GoalManager:            subs.goalManager,
		GoalDriver:             subs.goalDriver,
		MCP:                    subs.mcpManager,
		ReloadHandler:          &ReloadHandler{subs: subs},
		PTYManager:             subs.ptyMgr,
		LoopDetector:           loopDetectorFrom(subs),
		Steering:               steeringQueueFrom(subs),
	}

	if app != nil {
		wireInteractiveCallbacks(&ctx, subs, app)
	}
	return ctx
}

func wireInteractiveCallbacks(ctx *core.Context, subs *subsystems, app *App) {
	ctx.SelectOptionFunc = func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
		ch := subs.tuiEngine.ShowSelector(title, options, current)
		go func() {
			selected := <-ch
			if onSelected != nil {
				app.apply(func() { onSelected(selected, selected != "") })
			}
		}()
	}
	ctx.SelectOptionKeyedFunc = func(title string, options []tui.SelectorItem, current string, keys tui.SelectorKeymap, onSelected func(string, bool)) {
		ch := subs.tuiEngine.ShowSelectorKeyed(title, options, current, keys)
		go func() {
			selected := <-ch
			if onSelected != nil {
				app.apply(func() { onSelected(selected, selected != "") })
			}
		}()
	}
	wireAsyncSelectCallback(ctx, subs, app)
	// Multi-select confirm (M6 §7 step 3): same async contract as the
	// selector — show on the command loop via ShowConfirmMulti's overlay,
	// deliver on app.apply so callbacks re-enter command-loop state safely.
	ctx.ConfirmMultiFunc = func(title, body string, options []tui.ConfirmOption, defaultID string, allowCancel bool, onDone func(tui.MultiConfirmResult)) {
		ch, _ := subs.tuiEngine.ShowConfirmMulti(title, body, options, defaultID, allowCancel)
		go func() {
			res := <-ch
			if onDone != nil {
				app.apply(func() { onDone(res) })
			}
		}()
	}
	wireInputCallbacks(ctx, subs, app)
	ctx.ClarifyFunc = func(card *tui.ClarifyCard) (string, bool) {
		return app.clarify(card)
	}
	ctx.SubmitToAgent = func(text string) {
		subs.chat.AddUserMessage(text)
		subs.tuiEngine.RequestRender()
		app.sendToAgent(text)
	}
	ctx.RenderChat = func(width int) string {
		return dumpChat(subs, width)
	}
	ctx.ShowPTYOverlay = func(sessionID string) {
		pv := tui.NewPTYView(subs.ptyMgr, sessionID)
		pv.SetTUI(subs.tuiEngine)
		opts := tui.OverlayOptions{
			Width:        0,
			Height:       0,
			CaptureInput: true,
		}
		subs.tuiEngine.ShowOverlay(pv, opts)
	}
}

// wireInputCallbacks wires the Context input-editor callbacks: single-line
// prompts, main-input requests, and editor text prefill (used by /fork to
// stage the selected message for edit + resend).
func wireInputCallbacks(ctx *core.Context, subs *subsystems, app *App) {
	ctx.ShowInputFunc = func(prompt, current string, onSubmit func(string, bool)) {
		if inp := subs.getInput(); inp != nil {
			inp.SetText(current)
		}
		app.requestMainInputWithCancel(prompt, func(text string) {
			if onSubmit != nil {
				onSubmit(text, true)
			}
		}, func() {
			if onSubmit != nil {
				onSubmit("", false)
			}
		})
	}
	ctx.RequestMainInput = func(prompt string, onSubmit func(string)) {
		app.requestMainInput(prompt, onSubmit)
	}
	ctx.SetEditorTextFunc = func(text string) {
		app.apply(func() {
			if inp := subs.getInput(); inp != nil {
				inp.SetText(text)
			}
		})
	}
}

// wireAsyncSelectCallback wires Context.SelectOptionAsyncFunc: it shows a
// loading placeholder immediately, fetches items in a goroutine, and swaps
// them in on the command loop — keeping the UI responsive while a remote list
// (e.g. a provider's GET /models) is retrieved.
func wireAsyncSelectCallback(ctx *core.Context, subs *subsystems, app *App) {
	ctx.SelectOptionAsyncFunc = func(title string, fetch func() []tui.SelectorItem, onSelected func(string, bool)) {
		sel, ch := subs.tuiEngine.ShowSelectorLoading(title, "Loading…")
		go func() {
			items := fetch()
			subs.tuiEngine.Apply(func() {
				if len(items) > 0 {
					sel.SetItems(items)
				} else {
					sel.SetItems([]tui.SelectorItem{{Value: "", Label: "(no items)", Description: "fetch returned nothing"}})
				}
			})
		}()
		go func() {
			selected := <-ch
			if onSelected != nil {
				app.apply(func() { onSelected(selected, selected != "") })
			}
		}()
	}
}

// steeringQueueFrom returns the session steering queue from the agent manager.
func steeringQueueFrom(subs *subsystems) *core.SteeringQueue {
	if subs.agentMgr == nil {
		return nil
	}
	return subs.agentMgr.SteeringQueue()
}

// lastAssistantText returns the last assistant message text when the chat
// viewport is available, otherwise an empty string.
func lastAssistantText(subs *subsystems) string {
	if subs.chat == nil {
		return ""
	}
	return subs.chat.LastAssistantText()
}

// dumpChat renders the current chat viewport for the /dump command. When the
// TUI engine is not running (e.g. headless export), it returns an empty string.
func dumpChat(subs *subsystems, width int) string {
	if subs.tuiEngine == nil {
		return ""
	}
	if width <= 0 {
		width = 80
	}
	lines := subs.tuiEngine.RenderNow()
	var b strings.Builder
	for _, line := range lines {
		b.WriteString(ansi.Strip(line))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n")
}

// loopDetectorFrom returns the session loop detector from the agent manager.
func loopDetectorFrom(subs *subsystems) *core.LoopDetector {
	if subs.agentMgr == nil {
		return nil
	}
	return subs.agentMgr.LoopDetector()
}

// makeToolFactory returns a factory that creates configurable tool instances
// on demand when the user enables them at runtime via /tools:name:on.
func makeToolFactory(subs *subsystems) func(name string) (agentic.Tool, bool) {
	builders := runtimeToolBuilders(subs)
	return func(name string) (agentic.Tool, bool) {
		build, ok := builders[name]
		if !ok {
			return nil, false
		}
		return build()
	}
}

// runtimeToolBuilders maps every runtime-constructible configurable tool to its
// constructor. A table (not a switch) keeps the factory open for extension: a
// new re-enablable tool is one entry plus its constructor, and the enable path
// (/config → Tools, /tools:<name>:on, /docs …:on) picks it up unchanged.
func runtimeToolBuilders(subs *subsystems) map[string]func() (agentic.Tool, bool) {
	always := func(build func() agentic.Tool) func() (agentic.Tool, bool) {
		return func() (agentic.Tool, bool) { return build(), true }
	}
	return map[string]func() (agentic.Tool, bool){
		"bg_exec":           always(func() agentic.Tool { return makeBGExecTool(subs) }),
		"memento":           always(func() agentic.Tool { return makeMementoTool(subs) }),
		"python":            always(func() agentic.Tool { return makePythonTool(subs) }),
		"ssh_bash":          always(func() agentic.Tool { return makeSSHBashTool(subs) }),
		"verify":            always(func() agentic.Tool { return makeVerifyTool(subs.projectDir) }),
		"ask_user_question": always(func() agentic.Tool { return newAskUserQuestionTool(subs) }),
		"run_code": always(func() agentic.Tool {
			return makeRunCodeTool(subs.toolRegistry, subs.projectDir, subs.cfg)
		}),
		"agent": always(func() agentic.Tool {
			return newAgentTool(subs.agentPool, subs.modeRegistry, subs.taskBus, subs.agentMgr)
		}),
		"agent_swarm": always(func() agentic.Tool {
			return newAgentSwarmTool(subs.agentPool, subs.modeRegistry, subs.swarmState, subs.taskBus, subs.agentMgr, subs.events)
		}),
		"goa": func() (agentic.Tool, bool) {
			if subs.goaTool == nil {
				return nil, false
			}
			return subs.goaTool, true
		},
		"terminals":      func() (agentic.Tool, bool) { return makeTerminalsTool(subs) },
		"webfetch":       func() (agentic.Tool, bool) { return makeWebFetchToolWithSummarizer(subs) },
		"goal":           func() (agentic.Tool, bool) { return makeGoalToolRuntime(subs) },
		"lsp":            func() (agentic.Tool, bool) { return makeLSPToolRuntime(subs) },
		"request_review": func() (agentic.Tool, bool) { return makeAgentDrivenTool(subs, "request_review") },
		"delegate_to":    func() (agentic.Tool, bool) { return makeAgentDrivenTool(subs, "delegate_to") },
		"todo_list": func() (agentic.Tool, bool) {
			if subs.goalManager == nil {
				return nil, false
			}
			return &tools.TodoListTool{Mode: subs.goalManager.Mode}, true
		},
	}
}

// makeWebFetchToolWithSummarizer builds webfetch for the runtime factory and
// wires the sub-agent summarizer pool the startup path attaches separately
// (the pool may not exist yet when the tool is first registered).
func makeWebFetchToolWithSummarizer(subs *subsystems) (agentic.Tool, bool) {
	tool, ok := makeWebFetchTool(subs.sessionStore, subs.cfg, subs.projectDir)
	if !ok {
		return nil, false
	}
	if subs.agentPool != nil {
		attachWebFetchSummarizerTo(tool, &webFetchAgentPool{pool: subs.agentPool})
	}
	return tool, true
}

// newAskUserQuestionTool builds ask_user_question with the SAME interactive
// clarify hook the startup registration receives: without it a runtime-built
// instance could never reach the user.
func newAskUserQuestionTool(subs *subsystems) agentic.Tool {
	tool := &ask.AskUserQuestionTool{}
	if subs.clarifyFn != nil {
		tool.SetClarify(subs.clarifyFn)
	}
	return tool
}

// liveToolSetProvider returns the core.Context.LiveTools hook: the mode-
// filtered view of the live registry — literally the call startSession makes
// (filterToolsForCurrentMode, prompt.go is the single source of that filter).
// Runtime tool toggles push this set, so they can never advertise a tool the
// active mode disallows.
func liveToolSetProvider(subs *subsystems) func() []agentic.Tool {
	return func() []agentic.Tool {
		if subs.toolRegistry == nil {
			return nil
		}
		return filterToolsForCurrentMode(subs, subs.toolRegistry.All())
	}
}

// toolRegistryFor exposes the registry to commands without ever handing out a
// typed-nil pointer dressed as a live registry (a nil interface and a nil
// *tools.ToolRegistry must both read as "no registry").
func toolRegistryFor(subs *subsystems) core.ToolRegistry {
	if subs.toolRegistry == nil {
		return nil
	}
	return subs.toolRegistry
}

// skillRegistryFor is the same guard for the skill registry: commands (the
// /config root page reads the skills summary) nil-check the interface, which a
// typed-nil *skills.SkillRegistry would defeat and then panic on.
func skillRegistryFor(subs *subsystems) core.SkillRegistry {
	if subs.skillRegistry == nil {
		return nil
	}
	return subs.skillRegistry
}

// makeToolTeardown returns the /tools:name:off hook tearing integrations
// bound to a tool. For "lsp" it fully disables the integration: detaches the
// manager from read/edit/write and closes every running server
// Issue LSP — off must mean off, including background spawns).
func makeToolTeardown(subs *subsystems) func(name string) {
	return func(name string) {
		if name != "lsp" {
			return
		}
		unwireLSPFromFileTools(subs.toolRegistry)
		if subs.lspMgr != nil {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			_ = subs.lspMgr.Close(ctx)
			subs.lspMgr = nil
		}
	}
}

// makeLSPToolRuntime builds the lsp tool for the /tools:lsp:on runtime path.
// When LSP was fully off at bootstrap (tools.enabled.lsp: false → no manager,
// per Issue LSP), the manager is created now and wired into the
// already-registered read/edit/write tools so the whole integration comes up
// without a restart. Returns false only when the manager cannot exist (global
// lsp: false in config).
func makeLSPToolRuntime(subs *subsystems) (agentic.Tool, bool) {
	mgr := subs.lspMgr
	if mgr == nil {
		mgr = newLSPManager(subs.projectDir, subs.cfg)
		if mgr == nil {
			return nil, false
		}
		subs.lspMgr = mgr
		wireLSPIntoFileTools(subs.toolRegistry, mgr)
	}
	if !mgr.Started() {
		return nil, false
	}
	return &tools.LSPTool{
		WorktreeMgr: subs.worktreeMgr,
		ProjectDir:  subs.projectDir,
		Manager:     mgr,
	}, true
}

// wireLSPIntoFileTools attaches the LSP manager to the registered
// read/edit/write tools (used when the manager is created after bootstrap via
// /tools:lsp:on). unwireLSPFromFileTools detaches it (/tools:lsp:off).
func wireLSPIntoFileTools(reg *tools.ToolRegistry, mgr *lsp.Manager) {
	setLSPOnFileTools(reg, mgr)
}

// unwireLSPFromFileTools detaches the LSP manager from read/edit/write tools.
func unwireLSPFromFileTools(reg *tools.ToolRegistry) {
	setLSPOnFileTools(reg, nil)
}

// setLSPOnFileTools assigns the LSP manager (or nil) on every registered file
// tool that supports LSP linking.
func setLSPOnFileTools(reg *tools.ToolRegistry, mgr tools.LSPDocumentManager) {
	if reg == nil {
		return
	}
	if t, ok := reg.Get("read"); ok {
		if rt, ok := t.(*tools.ReadFileTool); ok {
			rt.LSPManager = mgr
		}
	}
	if t, ok := reg.Get("write"); ok {
		if wt, ok := t.(*tools.WriteFileTool); ok {
			wt.LSPManager = mgr
		}
	}
	if t, ok := reg.Get("edit"); ok {
		if et, ok := t.(*tools.EditFileTool); ok {
			et.LSPManager = mgr
		}
	}
}

// makeGoalToolRuntime builds the goal tool for the /tools:goal:on runtime
// path. The create gate is the same LIVE closure used at startup
// (goalCreateGate: cfg.Tools.Enabled.Goal OR --goal), read at every call — so
// a tool built while the flag was off still allows `create` once the flag is
// flipped (/tools:goal:on, /config → Tools, /config:set) with no restart.
func makeGoalToolRuntime(subs *subsystems) (agentic.Tool, bool) {
	if subs.goalManager == nil {
		return nil, false
	}
	return newGoalTool(subs.goalManager, goalCreateGate(subs.cfg, subs.opts), subs.cfg.Goals.AutoUnblockEnabled, subs.cfg.Goals.FreshContextEnabled,
		func() time.Duration { return subs.cfg.Goals.VerifyTimeoutOr(defaultGoalVerifyTimeout) }), true
}

func makeBGExecTool(subs *subsystems) agentic.Tool {
	if subs.bgMgr != nil {
		return tools.NewBGExecToolWithManager(subs.bgMgr)
	}
	return tools.NewBGExecTool()
}

func makeMementoTool(subs *subsystems) agentic.Tool {
	return &tools.MementoTool{ProjectDir: subs.projectDir, GlobalDir: subs.cfg.ConfigDir}
}

func makeSSHBashTool(subs *subsystems) agentic.Tool {
	return &tools.SSHBashTool{Hosts: sshHosts(subs.cfg)}
}

func makePythonTool(subs *subsystems) agentic.Tool {
	return &tools.PythonTool{
		TimeoutSeconds: subs.cfg.Tools.Python.TimeoutSeconds,
		ProjectDir:     subs.projectDir,
		Jail:           subs.cfg.Tools.Python.Jail || subs.cfg.DefaultModeState().Autonomy == internal.AutonomySolo,
	}
}

func makeTerminalsTool(subs *subsystems) (agentic.Tool, bool) {
	if subs.ptyMgr == nil {
		return nil, false
	}
	return &tools.TerminalsTool{
		Mgr:        subs.ptyMgr,
		Blocked:    subs.cfg.Tools.Terminal.Sandbox.BlockedCommands,
		Allowed:    subs.cfg.Tools.Terminal.Sandbox.AllowedCommands,
		Bypass:     !subs.cfg.Tools.Terminal.Sandbox.Enabled,
		ProjectDir: subs.projectDir,
	}, true
}

func makeAgentDrivenTool(subs *subsystems, name string) (agentic.Tool, bool) {
	if subs.agentPool == nil {
		return nil, false
	}
	toolsList := multiagent.AgentDrivenTools(subs.foregroundOrch, subs.agentPool)
	for _, t := range toolsList {
		if t.Schema().Name == name {
			return t, true
		}
	}
	return nil, false
}
