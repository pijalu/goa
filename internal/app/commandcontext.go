// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tools"
	"github.com/pijalu/goa/tui"
)

// coreContextForCommand builds the core.Context passed to slash commands.
func coreContextForCommand(subs *subsystems, app *App) core.Context {
	return core.Context{
		Config:                 subs.cfg,
		ProjectDir:             subs.projectDir,
		InitialActiveProvider:  subs.cfg.ActiveProvider,
		InitialActiveModel:     subs.cfg.ActiveModel,
		AgentManager:           subs.agentMgr,
		ExecutionController:    subs.execCtrl,
		ToolRegistry:           subs.toolRegistry,
		ToolFactory:            makeToolFactory(subs),
		SkillRegistry:          subs.skillRegistry,
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
		SkillSubAgentRunner:    &skillSubAgentRunner{pool: subs.agentPool},
		WorkflowRegistry:       subs.workflowReg,
		GoalManager:            subs.goalManager,
		ReloadHandler:          &ReloadHandler{subs: subs},
		PTYManager:             subs.ptyMgr,
		SelectOptionFunc: func(title string, options []tui.SelectorItem, current string, onSelected func(string, bool)) {
			ch := subs.tuiEngine.ShowSelector(title, options, current)
			go func() {
				selected := <-ch
				if onSelected != nil {
					app.apply(func() { onSelected(selected, selected != "") })
				}
			}()
		},
		ShowInputFunc: func(prompt, current string, onSubmit func(string, bool)) {
			// Input discipline (docs/TUI.md): route ALL text input through the main
			// input line (with the editor title set to the prompt) rather than a
			// throwaway overlay Input. This preserves the (value, ok) contract:
			// non-empty submit => ok=true; cancel (empty/Ctrl+C) => ok=false.
			// The callbacks run later on the commandLoop, so no apply() wrapper.
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
			}, true)
		},
		RequestMainInput: func(prompt string, onSubmit func(string)) {
			app.requestMainInput(prompt, onSubmit)
		},
		ClarifyFunc: func(card *tui.ClarifyCard) (string, bool) {
			return app.clarify(card)
		},
		SubmitToAgent: func(text string) {
			subs.chat.AddUserMessage(text)
			subs.tuiEngine.RequestRender()
			app.sendToAgent(text)
		},
		RenderChat: func(width int) string {
			return dumpChat(subs, width)
		},
		ShowPTYOverlay: func(sessionID string) {
			pv := tui.NewPTYView(subs.ptyMgr, sessionID)
			pv.SetTUI(subs.tuiEngine)
			opts := tui.OverlayOptions{
				Width:        0,
				Height:       0,
				CaptureInput: true,
			}
			subs.tuiEngine.ShowOverlay(pv, opts)
		},
		LoopDetector: loopDetectorFrom(subs),
	}
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
	return func(name string) (agentic.Tool, bool) {
		cfg := subs.cfg
		switch name {
		case "bg_exec":
			return tools.NewBGExecTool(), true
		case "memento":
			return &tools.MementoTool{ProjectDir: subs.projectDir, GlobalDir: cfg.ConfigDir}, true
		case "ssh_bash":
			return &tools.SSHBashTool{Hosts: sshHosts(cfg)}, true
		case "pty_exec":
			if subs.ptyMgr == nil {
				return nil, false
			}
			return &tools.PTYExecTool{Mgr: subs.ptyMgr}, true
		case "request_review", "delegate_to":
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
		return nil, false
	}
}
