// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/core"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui"
	"github.com/pijalu/goa/tui/agentctx"
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

// routeSteering checks the command, active-delegation, workflow, orchestrator,
// and main-agent steering paths in order. It returns true if the input was
// consumed as steering.
func (a *App) routeSteering(engine *tui.TUI, chat *tui.ChatViewport, text string) bool {
	if strings.HasPrefix(text, "/") {
		return false
	}
	if a.maybeSteerCommand(engine, chat, text) {
		return true
	}
	// The ACTIVE tab owns the input first (T5): typing on a running
	// delegation's tab steers that delegation. Everything else falls through
	// to the workflow/orchestrator/main-agent paths ("all").
	if a.maybeSteerActiveDelegation(engine, chat, text) {
		return true
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

// maybeSteerActiveDelegation routes user input to the ACTIVE multi-agent
// tab's delegation while it is running: the queue is drained by the delegated
// agent between stream rounds and woven into its current turn. Non-running or
// non-delegation tabs return false so the normal steering/dispatch paths run
// (the "fall back to all" behavior).
func (a *App) maybeSteerActiveDelegation(engine *tui.TUI, chat *tui.ChatViewport, text string) bool {
	subs := a.subs
	reg := subs.agentRegistry
	if reg == nil || subs.foregroundOrch == nil {
		return false
	}
	id, _ := reg.Active()
	if id == "" || id == agentctx.MainAgentID {
		return false
	}
	if a.delegationStatus(id) != multiagent.DelegationRunning {
		return false
	}
	if !subs.foregroundOrch.SteerDelegation(id, text) {
		return false
	}
	if subs.steeringChrome != nil {
		subs.steeringChrome.Add(text)
	}
	engine.RequestRender()
	return true
}

// maybeSteerCommand buffers user input as steering while an async (long-running)
// slash command is executing in the background. The enqueued text is delivered
// as a follow-up agent message when the command completes (see
// dispatchCommandSteering). It reuses the same steering queue and chrome as
// agent-turn steering so the UI is consistent. Returns true if the input was
// consumed as steering.
func (a *App) maybeSteerCommand(engine *tui.TUI, chat *tui.ChatViewport, text string) bool {
	subs := a.subs
	if !a.commandBusy || subs.agentMgr == nil {
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

	// Long-running commands (e.g. /compress:summarize) opt into async
	// execution: Run in a background goroutine with a dedicated spinner,
	// keeping the UI responsive and the input line live for steering. Doc
	// suffixes (/cmd:?, /cmd:??) are instant and bypass this path.
	if result.DocLevel == core.DocSuffixNone && result.Command != nil {
		if label := core.AsyncHintOf(result.Command, result.Args); label != "" {
			a.runAsyncCommand(result, input, trimmed, label)
			return
		}
	}

	// Immediate feedback for slow commands ("Session: slow commands
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

// runAsyncCommand launches a long-running command in a background goroutine,
// showing a dedicated spinner and keeping the input line live so the user can
// enqueue steering until completion. On completion it clears the spinner,
// renders the result, and delivers any enqueued steering as a follow-up agent
// message. Guards against concurrent async commands: a second invocation while
// one is running is rejected with a clear message rather than racing on shared
// state (e.g. two concurrent compressions corrupting history).
func (a *App) runAsyncCommand(result *core.RouteResult, input, trimmed, label string) {
	subs := a.subs

	if a.commandBusy {
		subs.chat.AddSystemMessage(fmt.Sprintf("> %s", input))
		subs.chat.AddSystemMessage("Another command is already running — please wait for it to finish.")
		subs.tuiEngine.RequestRender()
		return
	}

	a.commandBusy = true

	// Show the spinner immediately so there is no silent gap between submit
	// and the background work becoming visible.
	subs.statusMsg.Reset()
	subs.statusMsg.Show(label)
	subs.tuiEngine.RequestRender()

	ctx := coreContextForCommand(subs, a)

	go func() {
		var (
			output string
			err    error
		)
		func() {
			defer func() {
				if r := recover(); r != nil {
					err = fmt.Errorf("command panicked: %v", r)
				}
			}()
			output, err = subs.cmdRouter.Execute(ctx, result)
		}()

		a.apply(func() {
			a.commandBusy = false
			subs.statusMsg.Clear()
			if err != nil {
				output = fmt.Sprintf("Error: %v", err)
			}
			a.postCommandBookkeeping(result, trimmed)
			a.echoCommandResult(result, input, output)
			a.dispatchCommandSteering()
			subs.tuiEngine.RequestRender()
		})
	}()
}

// dispatchCommandSteering delivers any free-text enqueued during an async
// command as a follow-up agent message. It clears the steering chrome and
// queue, then sends the joined text to the agent so the user's mid-command
// input is not lost. No-op when nothing was enqueued.
func (a *App) dispatchCommandSteering() {
	subs := a.subs
	if subs.agentMgr == nil {
		return
	}
	sq := subs.agentMgr.SteeringQueue()
	if sq == nil {
		return
	}
	pending := sq.Flush()
	if len(pending) == 0 {
		return
	}
	text := strings.Join(pending, "\n\n")
	if subs.steeringChrome != nil {
		subs.steeringChrome.Clear()
	}
	a.displayUserMessage(subs.chat, text, nil)
	a.sendToAgent(text)
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
		// Finalize any in-progress streaming block before the command result
		// lands, so the result is appended AFTER a complete block and the
		// next streamed content starts a fresh block after it. Without this,
		// a screen-filling result (e.g. /goal:list with complete objectives)
		// echoed mid-stream leaves the streaming block buried under it; the
		// stream keeps growing that off-screen block, and every chunk forces
		// the compositor's mid-transcript scrollback-reset path — CPU >100%
		// and repeated terminal viewport yanks until a new block finally
		// starts after the result. Ending the block here makes the very next
		// stream chunk start after the result (bottom append — the fast,
		// O(viewport) path).
		a.endCurrentStream()
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
