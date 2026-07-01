// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"os/exec"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tui"
)

func (a *App) makeSubmitHandler(engine *tui.TUI, chat *tui.ChatViewport) func(string) {
	return func(text string) {
		text = strings.TrimSpace(text)
		if text == "" {
			if a.pendingInput != nil {
				a.cancelPendingMainInput()
				engine.RequestRender()
				return
			}
			engine.RequestRender()
			return
		}

		if a.handlePendingMainInput(text) {
			return
		}

		if strings.HasPrefix(text, "!") {
			a.handleBangCommand(engine, chat, text)
			return
		}

		if !strings.HasPrefix(text, "/") && a.maybeSteerWorkflow(engine, chat, text) {
			return
		}

		a.dispatchUserSubmit(engine, chat, text)
	}
}

// handlePendingMainInput consumes a value for a command that is waiting on
// the main input line. It returns true when the input was handled.
//
// The pending request captures the next non-empty submission as its value.
// A slash-prefixed string is NOT treated as cancellation, because the value
// (e.g. a goal objective) may legitimately start with "/" (file paths, etc.).
// Empty input cancels via Editor.submit's early return; the prompt text also
// documents "empty to cancel".
func (a *App) handlePendingMainInput(text string) bool {
	if a.pendingInput == nil {
		return false
	}
	onSubmit := a.pendingInput.onSubmit
	a.clearMainInputRequest()
	onSubmit(text)
	return true
}

// dispatchUserSubmit routes a normal user submission to either a slash command
// or the agent.
func (a *App) dispatchUserSubmit(engine *tui.TUI, chat *tui.ChatViewport, text string) {
	isCmd := strings.HasPrefix(text, "/")
	messageText, images := splitUserInput(text)
	if !isCmd {
		a.displayUserMessage(chat, messageText, images)
	}
	engine.RequestRender()
	if isCmd {
		a.handleSlashCommand(text)
	} else {
		a.sendToAgentWithImages(messageText, images)
	}
}

func splitUserInput(text string) (string, []string) {
	images := extractImagePaths(text)
	messageText := stripImagePaths(text)
	return messageText, images
}

func (a *App) displayUserMessage(chat *tui.ChatViewport, text string, images []string) {
	if text != "" {
		chat.AddUserMessage(text)
	}
	for _, img := range images {
		chat.AddSystemMessage(fmt.Sprintf("[attached image: %s]", img))
	}
}

func (a *App) maybeSteerWorkflow(engine *tui.TUI, chat *tui.ChatViewport, text string) bool {
	subs := a.subs
	if subs.foregroundOrch == nil {
		return false
	}
	progress := subs.foregroundOrch.Progress()
	if progress.Status != "running" && progress.Status != "gate" {
		return false
	}
	chat.AddSystemMessage(fmt.Sprintf("[steering] %s", text))
	subs.foregroundOrch.InjectSteering(text)
	engine.RequestRender()
	return true
}

func (a *App) handlePastedImage(engine *tui.TUI, chat *tui.ChatViewport, path string) {
	subs := a.subs
	if inp := subs.getInput(); inp != nil {
		inp.InsertTextAtCursor(" " + path)
		engine.RequestRender()
	}
}

func (a *App) handleBangCommand(engine *tui.TUI, chat *tui.ChatViewport, text string) {
	subs := a.subs
	isNote := strings.HasPrefix(text, "!!")
	cmdStr := strings.TrimSpace(text[1:])
	if strings.HasPrefix(cmdStr, "!") {
		cmdStr = strings.TrimSpace(cmdStr[1:])
	}
	if cmdStr == "" {
		return
	}
	chat.AddSystemMessage("$ " + cmdStr)
	engine.RequestRender()
	go func() {
		cmd := exec.Command("bash", "-c", cmdStr)
		output, err := cmd.Output()
		outStr := strings.TrimSpace(string(output))
		if err != nil {
			outStr = fmt.Sprintf("Error: %v\n%s", err, outStr)
		}
		if isNote {
			chat.AddSystemMessage("```\n" + outStr + "\n```")
			engine.RequestRender()
		} else {
			if inp := subs.getInput(); inp != nil {
				inp.SetText("```\n" + outStr + "\n```")
				engine.SetFocus(inp)
				engine.RequestRender()
			}
		}
	}()
}

func (a *App) sendToAgent(input string) {
	a.sendToAgentWithImages(input, nil)
}

func (a *App) sendToAgentWithImages(input string, images []string) {
	subs := a.subs
	if subs.agentMgr == nil {
		return
	}
	a.markSessionActive()

	modelName := a.resolveModelName()
	input = a.expandSkillInput(input)

	a.showSendingStatus(modelName)
	if err := subs.agentMgr.SendUserInputWithImages(input, images); err != nil {
		a.handleSendError(err)
	}
}

// extractImagePaths returns paths that look like pasted image files.
// It preserves line structure so callers can rebuild multi-line text.
func extractImagePaths(text string) []string {
	var paths []string
	for _, line := range strings.Split(text, "\n") {
		for _, field := range strings.Fields(line) {
			lower := strings.ToLower(field)
			if isImagePath(lower) {
				paths = append(paths, field)
			}
		}
	}
	return paths
}

// stripImagePaths removes pasted image paths from text while preserving
// original line breaks and spacing within each line.
func stripImagePaths(text string) string {
	lines := strings.Split(text, "\n")
	for i, line := range lines {
		fields := strings.Fields(line)
		var kept []string
		for _, field := range fields {
			lower := strings.ToLower(field)
			if isImagePath(lower) {
				continue
			}
			kept = append(kept, field)
		}
		lines[i] = strings.Join(kept, " ")
	}
	return strings.Join(lines, "\n")
}

func isImagePath(lower string) bool {
	return strings.HasSuffix(lower, ".png") || strings.HasSuffix(lower, ".jpg") ||
		strings.HasSuffix(lower, ".jpeg") || strings.HasSuffix(lower, ".webp") ||
		strings.HasSuffix(lower, ".gif")
}

func (a *App) markSessionActive() {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	a.sessionActive = true
}

func (a *App) resolveModelName() string {
	subs := a.subs
	providerCfg, _ := subs.providerMgr.Active()
	modelName := subs.cfg.ActiveModel
	if providerCfg != nil {
		modelName = subs.providerMgr.ResolveModelName(*providerCfg, subs.cfg.ActiveModel)
	}
	return modelName
}

func (a *App) expandSkillInput(input string) string {
	subs := a.subs
	if !strings.HasPrefix(input, "/skill:") || subs.skillRegistry == nil {
		return input
	}
	name, _, _ := skills.ParseSkillCommand(input)
	if name != "" {
		if _, ok := subs.skillRegistry.Get(name); ok {
			subs.chat.AddSystemMessage(fmt.Sprintf("Loading [Skill: %s]", name))
		}
	}
	expanded := skills.ExpandSkillCommand(subs.promptReg, subs.skillRegistry, input)
	if expanded != input {
		return expanded
	}
	return input
}

func (a *App) showSendingStatus(modelName string) {
	subs := a.subs
	modelStr := modelDisplay(subs.cfg.ActiveProvider, modelName)
	subs.statusMsg.Show("Sending request...")
	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  modelStr,
		Profile:                subs.cfg.ActiveMajor(),
		Mode:                   string(subs.cfg.DefaultModeState().Autonomy),
		Activity:               "send",
		MainActivity:           "Sending request...",
		CompanionModel:         companionModelDisplay(subs),
		Provider:               subs.cfg.ActiveProvider,
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
	subs.footer.SetModelBusy(true)
	subs.tuiEngine.RequestRender()
}

func (a *App) handleSendError(err error) {
	subs := a.subs
	errMsg := fmt.Sprintf("send error: %v", err)
	subs.chat.AddSystemMessage(errMsg)
	a.flashError(errMsg)
	subs.statusMsg.Show(subs.cfg.ActiveModel + " | idle")
	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  activeModelDisplay(subs),
		Profile:                subs.cfg.ActiveMajor(),
		Mode:                   string(subs.cfg.DefaultModeState().Autonomy),
		CompanionModel:         companionModelDisplay(subs),
		Provider:               subs.cfg.ActiveProvider,
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
	subs.footer.SetModelBusy(false)
	subs.tuiEngine.RequestRender()
}

func (a *App) flashError(msg string) {
	subs := a.subs
	if subs.events == nil {
		return
	}
	select {
	case subs.events.Chat <- event.ChatEvent{Flash: &event.Flash{Text: msg}}:
	default:
	}
}

func (a *App) handleSlashCommand(input string) {
	subs := a.subs
	trimmed := strings.TrimSpace(input)
	if trimmed == "/help" {
		a.handleHelpCommand()
		return
	}
	result := subs.cmdRouter.Parse(input)
	if result == nil {
		return
	}

	ctx := coreContextForCommand(subs, a)
	output, err := subs.cmdRouter.Execute(ctx, result)
	if err != nil {
		output = fmt.Sprintf("Error: %v", err)
	}
	// Record command usage (even if error — user attempted it)
	if subs.commandStats != nil {
		subs.commandStats.Record(trimmed)
		subs.commandStats.Save()
		if inp := subs.getInput(); inp != nil {
			inp.UpdateCommandFreqs(subs.commandStats.All())
		}
	}

	// Internal commands are not echoed into the chat viewport and never
	// forwarded to the LLM. They are purely in-process (e.g., /config opens
	// the wizard). The command itself is responsible for user feedback via
	// status messages, flash notifications, or the TUI event channel.
	if result.Command != nil && core.IsInternal(result.Command) {
		subs.tuiEngine.RequestRender()
		return
	}

	// A command that opened an interactive main-input prompt (e.g. /goal)
	// must not be echoed as "> /goal ... completed"; the prompt itself is
	// the user-facing feedback.
	if a.pendingInput != nil {
		subs.tuiEngine.RequestRender()
		return
	}

	if output != "" {
		subs.chat.AddSystemMessage(fmt.Sprintf("> %s", input))
		subs.chat.AddSystemMessage(output)
		subs.tuiEngine.RequestRender()
	}
}

func (a *App) handleHelpCommand() {
	subs := a.subs
	var b strings.Builder
	b.WriteString("# Goa Commands\n\n")
	for _, cmd := range core.GlobalRegistry().All() {
		name := cmd.Name()
		desc := cmd.ShortHelp()
		if desc == "" {
			desc = "no description"
		}
		b.WriteString(fmt.Sprintf("- **/%s** — %s\n", name, desc))
	}
	b.WriteString("\nType `/command:?` for short help, `/command:??` for long help.")
	subs.chat.AddSystemMessage(b.String())
	subs.tuiEngine.RequestRender()
}
