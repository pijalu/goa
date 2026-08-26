// SPDX-License-Identifier: GPL-3.0-or-later

package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/core/commands"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/skills"
	"github.com/pijalu/goa/tui"
)

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
	if subs.steeringChrome != nil {
		subs.steeringChrome.Add(text)
	}
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
// busy — a manager-owned user turn OR an externally driven turn such as a
// goal continuation (the goal driver runs agent.Run directly, so IsRunning
// alone misses it and the text would bypass the steering queue: no bubble, no
// mid-turn weaving, and possible loss if the in-flight turn errored). The
// queued input is woven into the current turn by the agent's between-round
// drain, or dispatched as a follow-up user message when the turn completes.
// Returns true if the input was consumed as steering.
func (a *App) maybeSteerAgent(engine *tui.TUI, chat *tui.ChatViewport, text string) bool {
	subs := a.subs
	if subs.agentMgr == nil || !subs.agentMgr.IsBusy() {
		return false
	}
	if sq := subs.agentMgr.SteeringQueue(); sq != nil {
		sq.Append(text)
	}
	if subs.steeringChrome != nil {
		subs.steeringChrome.Add(text)
	}
	engine.RequestRender()
	return true
}

// restoreSteeringToInput drains the main-agent steering queue back into the
// input line and clears the steering-pending bubble, so the user can edit /
// resend / discard text they typed mid-turn. It is a no-op when there is no
// agent, an empty queue, or no input editor. The restored text is NOT sent.
// Shared by Alt+E (handleEditSteering) and ESC (handleEscape) so the two paths
// cannot diverge (S1).
func (a *App) restoreSteeringToInput(chat *tui.ChatViewport) {
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
	if subs.steeringChrome != nil {
		subs.steeringChrome.Clear()
	}
}

// handleEditSteering moves pending steering text back into the input line
// for editing (Alt+E). The steering queue is emptied until the user
// resubmits; the pending bubble and footer indicator are cleared.
func (a *App) handleEditSteering(engine *tui.TUI, chat *tui.ChatViewport) {
	a.restoreSteeringToInput(chat)
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
	if subs.steeringChrome != nil {
		subs.steeringChrome.Add(text)
	}
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
	// Expand @[label](goa-session:<id>) cross-session references (P24 / CX7)
	// into a bounded, read-only, untrusted snapshot prepended to the model
	// message. Invalid references reject the send (dsh: any invalid
	// reference, failed read, or budget failure rejects before the host
	// calls followup).
	var err error
	input, err = expandSessionReferences(subs, input)
	if err != nil {
		a.handleSendError(err)
		return
	}

	a.showSendingStatus(modelName)
	if err := subs.agentMgr.SendUserInputWithImages(input, images); err != nil {
		a.handleSendError(err)
	}
}

// expandSessionReferences parses @[label](goa-session:<id>) mentions in the
// input, resolves each referenced session to a bounded read-only snapshot,
// and prepends the untrusted <referenced-sessions> warning frame to the
// model-facing message. The TUI bubble keeps showing the raw user text; the
// snapshot is model-facing only (dsh model-hidden display content). Returns
// the input unchanged when the session store is unavailable or no references
// are present.
func expandSessionReferences(subs *subsystems, input string) (string, error) {
	if subs.sessionStore == nil {
		return input, nil
	}
	currentSessionID := ""
	if subs.agentMgr != nil {
		currentSessionID = subs.agentMgr.SessionID()
	}
	rewritten, frame, err := subs.sessionStore.ResolveSessionReferenceMentions(input, currentSessionID)
	if err != nil {
		return "", err
	}
	if frame == "" {
		return rewritten, nil
	}
	return frame + "\n\n" + rewritten, nil
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
		Provider:               sessionProviderID(subs),
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
		Provider:               sessionProviderID(subs),
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
	subs.footer.SetModelBusy(false)
	subs.tuiEngine.RequestRender()
}
