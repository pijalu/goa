// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/internal/agentic"
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

		// Record in per-session input history before routing
		a.recordInputHistory(text)

		if a.handlePendingMainInput(text) {
			return
		}

		if strings.HasPrefix(text, "!") {
			a.handleBangCommand(engine, chat, text)
			return
		}

		if a.routeSteering(engine, chat, text) {
			return
		}

		a.dispatchUserSubmit(engine, chat, text)
	}
}

// routeSteering checks the workflow, orchestrator, and main-agent steering
// paths in order. It returns true if the input was consumed as steering.
func (a *App) routeSteering(engine *tui.TUI, chat *tui.ChatViewport, text string) bool {
	if strings.HasPrefix(text, "/") {
		return false
	}
	if a.maybeSteerWorkflow(engine, chat, text) {
		return true
	}
	if a.maybeSteerOrchestrator(engine, chat, text) {
		return true
	}
	if a.maybeSteerAgent(engine, chat, text) {
		return true
	}
	return false
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

func (a *App) maybeSteerOrchestrator(engine *tui.TUI, chat *tui.ChatViewport, text string) bool {
	subs := a.subs
	if subs.agentView == nil || !subs.agentView.Active() {
		return false
	}
	target := "all"
	if id := subs.agentView.ActiveAgentID(); id != "" {
		target = id
	}
	chat.AddSteeringPending(text)
	ctx := coreContextForCommand(subs, a)
	cmd := &commands.OrchestrateCommand{
		Builder:  subs.orchAdapter,
		Active:   subs.orchActive,
		RootDir:  filepath.Join(subs.projectDir, ".goa", "orchestrator"),
		GoalMode: subs.goalManager.Mode,
	}
	_ = cmd.Run(ctx, []string{"steer", "id=" + target, "message=" + text})
	engine.RequestRender()
	return true
}

// maybeSteerAgent buffers user input as steering while the main agent is
// running. The queued input is injected as a follow-up user message when the
// current turn completes. Returns true if the input was consumed as steering.
func (a *App) maybeSteerAgent(engine *tui.TUI, chat *tui.ChatViewport, text string) bool {
	subs := a.subs
	if subs.agentMgr == nil || !subs.agentMgr.IsRunning() {
		return false
	}
	if sq := subs.agentMgr.SteeringQueue(); sq != nil {
		sq.Append(text)
	}
	chat.AddSteeringPending(text)
	engine.RequestRender()
	return true
}

// handleEditSteering moves pending steering text back into the input line
// for editing (Alt+E). The steering queue is emptied until the user
// resubmits; the pending bubble and footer indicator are cleared.
func (a *App) handleEditSteering(engine *tui.TUI, chat *tui.ChatViewport) {
	subs := a.subs
	if subs.agentMgr == nil {
		return
	}
	sq := subs.agentMgr.SteeringQueue()
	if sq == nil || sq.Len() == 0 {
		return
	}
	pending := sq.Flush()
	text := strings.Join(pending, "\n\n")
	if inp := subs.getInput(); inp != nil {
		inp.SetText(text)
	}
	chat.ClearSteeringPending()
	engine.RequestRender()
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
	chat.AddSteeringPending(text)
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
	// Expand @file references to absolute paths so the model can read them.
	input = a.expandAtRefs(input)

	a.showSendingStatus(modelName)
	if err := subs.agentMgr.SendUserInputWithImages(input, images); err != nil {
		a.handleSendError(err)
	}
}

// recordInputHistory records a user input in the current session's input
// history file, enabling cross-session history reconstruction.
func (a *App) recordInputHistory(text string) {
	subs := a.subs
	if subs.agentMgr == nil || subs.sessionStore == nil {
		return
	}
	sessionID := subs.agentMgr.SessionID()
	if sessionID == "" {
		return
	}
	if err := subs.sessionStore.RecordInput(sessionID, text); err != nil {
		subs.logger.Log(agentic.Error, "failed to record input history: %v", err)
	}
}

// expandAtRefs replaces @-prefixed file references with the full absolute path
// so the model can read the file content. The @<path> pattern is resolved
// relative to the current working directory.
func (a *App) expandAtRefs(input string) string {
	if a.subs == nil || a.subs.projectDir == "" {
		return input
	}
	expanded := expandFileRefs(input, a.subs.projectDir)
	return expanded
}

// isWordChar reports whether a byte is a word character (letter, digit, or underscore).
func isWordChar(b byte) bool {
	return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') ||
		(b >= '0' && b <= '9') || b == '_'
}

// isWhitespace reports whether a byte is a space, tab, newline, or carriage return.
func isWhitespace(b byte) bool {
	return b == ' ' || b == '\t' || b == '\n' || b == '\r'
}

// extractAtPath extracts the path after an @ character. Returns the path and
// the end index in the input string.
func extractAtPath(input string, startIdx int) (path string, endIdx int) {
	pathEnd := startIdx
	for pathEnd < len(input) && !isWhitespace(input[pathEnd]) {
		pathEnd++
	}
	return input[startIdx:pathEnd], pathEnd
}

// resolveAtPath resolves a path from @<path> notation. If the path exists on
// disk, returns the absolute path; otherwise returns "" to signal no expansion.
func resolveAtPath(path, workdir string) string {
	resolved := path
	if !filepath.IsAbs(path) {
		resolved = filepath.Join(workdir, path)
	}
	if _, err := os.Stat(resolved); err == nil {
		return resolved
	}
	return ""
}

// expandFileRefs replaces @-prefixed file references in a string with the
// absolute path of the file. It only replaces references that look like
// @<path> where <path> is a valid filesystem path.
func expandFileRefs(input, workdir string) string {
	var result strings.Builder
	result.Grow(len(input))
	i := 0
	for i < len(input) {
		atIdx := strings.Index(input[i:], "@")
		if atIdx < 0 {
			result.WriteString(input[i:])
			break
		}
		absIdx := i + atIdx
		result.WriteString(input[i : i+atIdx])

		// Keep @ as-is when it's mid-word
		if absIdx > 0 && isWordChar(input[absIdx-1]) {
			result.WriteByte('@')
			i = absIdx + 1
			continue
		}

		path, pathEnd := extractAtPath(input, absIdx+1)
		if path == "" {
			result.WriteByte('@')
			i = absIdx + 1
			continue
		}

		if resolved := resolveAtPath(path, workdir); resolved != "" {
			result.WriteString(resolved)
		} else {
			result.WriteByte('@')
			result.WriteString(path)
		}
		i = pathEnd
	}
	return result.String()
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
	subs.statusMsg.Reset()
	subs.statusMsg.Show("Sending request...")
	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  modelStr,
		Profile:                string(subs.effectiveModeState().Major),
		Mode:                   string(subs.effectiveModeState().Autonomy),
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
		Profile:                string(subs.effectiveModeState().Major),
		Mode:                   string(subs.effectiveModeState().Autonomy),
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

	// Record non-internal slash commands in the session store so a session
	// that consists only of commands (e.g. /orchestrate) is not empty on reload.
	a.recordCommandInSessionStore(result, input)

	// Immediate feedback for slow commands (bugs.md "Session: slow commands
	// need an executing placeholder"): show the spinner line before the
	// synchronous Run so there is no silent gap between submit and output.
	showPlaceholder := a.beginCommandPlaceholder(result, trimmed)
	if showPlaceholder {
		// Panic guard: a panicking command must not leave the spinner stuck.
		defer subs.statusMsg.Clear()
	}

	ctx := coreContextForCommand(subs, a)
	output, err := subs.cmdRouter.Execute(ctx, result)

	if showPlaceholder {
		subs.statusMsg.Clear()
	}
	if err != nil {
		output = fmt.Sprintf("Error: %v", err)
	}

	a.postCommandBookkeeping(result, trimmed)
	a.echoCommandResult(result, input, output)
}

// postCommandBookkeeping applies post-execution side effects: pending input
// history (e.g. after /session:restore) and command-usage stats.
func (a *App) postCommandBookkeeping(result *core.RouteResult, trimmed string) {
	subs := a.subs
	// After command execution (e.g. /session:restore), apply any pending
	// input history to the editor.
	if subs.agentMgr != nil {
		if h := subs.agentMgr.GetAndClearPendingInputHistory(); len(h) > 0 {
			if inp := subs.getInput(); inp != nil {
				inp.SetHistory(h)
			}
		}
	}

	// Record command usage (even if error — user attempted it)
	if subs.commandStats != nil {
		subs.commandStats.Record(trimmed)
		subs.commandStats.Save()
		if inp := subs.getInput(); inp != nil {
			inp.UpdateCommandFreqs(subs.commandStats.All())
		}
	}
}

// echoCommandResult renders the command's output into the chat viewport.
// Internal commands (e.g. /config) and commands that opened an interactive
// main-input prompt (e.g. /goal) handle their own feedback and are not echoed.
func (a *App) echoCommandResult(result *core.RouteResult, input, output string) {
	subs := a.subs
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

// beginCommandPlaceholder shows an "executing /cmd ..." status line before a
// command's synchronous Run so there is no silent gap between submit and
// first feedback. It returns true when the placeholder was shown and the
// caller must Clear it after Execute. Doc-suffix lookups (/cmd:?, /cmd:??)
// and not-found parses are instant; only actual execution gets the
// placeholder.
func (a *App) beginCommandPlaceholder(result *core.RouteResult, trimmed string) bool {
	subs := a.subs
	if result.Command == nil || result.DocLevel != core.DocSuffixNone || subs.statusMsg == nil {
		return false
	}
	subs.statusMsg.Reset()
	subs.statusMsg.Show(fmt.Sprintf("executing %s ...", trimmed))
	subs.tuiEngine.RequestRender()
	return true
}

// recordCommandInSessionStore writes a synthetic user content event for
// non-internal slash commands so sessions that consist only of commands (e.g.
func (a *App) recordCommandInSessionStore(result *core.RouteResult, input string) {
	if result == nil || result.Command == nil || core.IsInternal(result.Command) {
		return
	}
	subs := a.subs
	if subs == nil || subs.sessionStore == nil {
		return
	}
	subs.sessionStore.WriteEvent(agentic.OutputEvent{
		Type: agentic.EventContent,
		Role: agentic.User,
		Text: input,
	})
}

func (a *App) handleHelpCommand() {
	subs := a.subs
	var b strings.Builder
	b.WriteString("# Goa Commands\n\n")
	for _, cmd := range subs.registry.All() {
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