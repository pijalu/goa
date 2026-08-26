// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/tooltracker"
	"github.com/pijalu/goa/tui"
)

func (a *App) resetCacheBustBaseline() {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	a.lastTurnCacheRead = 0
	a.cacheReadEstablished = false
}

func (a *App) handleProgressEvent(ev *agentic.OutputEvent) {
	// Show prompt-processing progress while waiting for the first token,
	// or reconnection/status messages.
	if ev.PromptProgress != nil {
		a.setWaitingForReplyStatus(ev.PromptProgress)
		return
	}
	if ev.Text != "" {
		a.subs.statusMsg.Show(ev.Text)
		a.subs.tuiEngine.RequestRender()
		return
	}
	// Empty text is the agent's progress-clear signal (emitted by
	// finishProcessing on every turn-exit path and by the stream retry
	// logic). Without this branch the "Sending request..." spinner would
	// linger on any exit path that skips EventEnd.
	a.subs.statusMsg.Clear()
	a.subs.tuiEngine.RequestRender()
}

func (a *App) handleStreamContent(ev *agentic.OutputEvent) {
	if ev.Role == agentic.User || ev.Role == agentic.System {
		a.handleUserOrSystemContent(ev)
		return
	}
	if ev.State == agentic.StateThinking {
		a.handleThinkingContent(ev)
		return
	}
	if ev.Text != "" {
		a.handleAssistantContent(ev)
	}
}

// handleUserOrSystemContent renders user/system-role stream events. Live user
// content is suppressed (the submit handler already rendered it) except for
// replayed history and mid-turn drained steering, both of which must appear.
func (a *App) handleUserOrSystemContent(ev *agentic.OutputEvent) {
	if ev.Role == agentic.User && ev.Text != "" {
		switch {
		case isSteeringDrained(ev):
			// Mid-turn steering was drained from the queue and woven into the
			// conversation: clear the pending bubble and show the consumed text
			// as a user message (the bubble's whole point was "this will send").
			if a.subs.steeringChrome != nil {
				a.subs.steeringChrome.Clear()
			}
			a.subs.chat.AddUserMessage(ev.Text)
		case isToolsetNotice(ev):
			// Host-generated toolset-change notice (AgentManager.SetTools).
			// The model receives it as a user message; the human sees it as a
			// system info line — it is not something the user typed.
			a.subs.chat.AddSystemMessage(ev.Text)
		case isReplay(ev):
			a.subs.chat.AddUserMessage(ev.Text)
		}
		return
	}
	if ev.Role == agentic.System && ev.Text != "" && isSystemNotification(ev) {
		a.endCurrentStream()
		// A stream-retry notification means the agent reset its content buffer
		// and will re-stream the answer from scratch. Retract the orphaned
		// in-progress assistant bubble so the partial pre-retry text does not
		// linger next to the re-streamed bubble (Issue 4 duplicates).
		if isStreamRetry(ev) {
			a.subs.chat.RemoveLastMessageOfType(tui.ConsoleAssistantMessage, tui.ConsoleThinkingBlock)
		}
		a.subs.chat.AddSystemMessage(ev.Text)
	}
}

// isToolsetNotice reports whether ev is the host-generated toolset-change
// notice injected by AgentManager.SetTools (user-role message tagged with
// agentic.MetaToolsetNotice). The TUI surfaces it as a system info line.
func isToolsetNotice(ev *agentic.OutputEvent) bool {
	return ev.Metadata != nil && ev.Metadata[agentic.MetaToolsetNotice] == "true"
}

// isStreamRetry reports whether ev is the agent's stream-retry notification,
// which signals that the in-progress assistant stream is being discarded and
// re-generated from the start.
func isStreamRetry(ev *agentic.OutputEvent) bool {
	return ev.Metadata != nil && ev.Metadata["stream_retry"] == "true"
}

// isSteeringDrained reports whether ev is a user message the agent wove into
// the turn from the mid-turn steering queue. The pending steering bubble must
// be cleared when this arrives — the queue is now empty, so there is nothing
// left "to send", and leaving the bubble up would show stale steering.
func isSteeringDrained(ev *agentic.OutputEvent) bool {
	return ev.Metadata != nil && ev.Metadata["steering_drained"] == "true"
}

// isSystemNotification reports whether ev is a UI-only system message (e.g.
// a retry notification) that should be rendered as a chat bubble.
func isSystemNotification(ev *agentic.OutputEvent) bool {
	return ev.Metadata != nil && ev.Metadata["category"] == "system-notification"
}

// isReplay reports whether ev is a restored session event, as opposed to a
// live event. During replay, stored user content events are rendered so the
// chat viewport reconstructs the full conversation; live user content is
// already added by the submit handler and must stay suppressed.
func isReplay(ev *agentic.OutputEvent) bool {
	return ev.Metadata != nil && ev.Metadata["replay"] == "true"
}

func (a *App) handleThinkingContent(ev *agentic.OutputEvent) {
	if a.subs.cfg != nil && !a.subs.cfg.TUI.Transparency.ShowThinking {
		return
	}
	a.endStreamIfDifferent(agentic.StateThinking)
	if !a.stream.is(tui.ConsoleThinkingBlock) {
		a.stream.begin(tui.ConsoleThinkingBlock)
		expanded := a.subs.cfg == nil || !a.subs.cfg.TUI.Transparency.ThinkingCollapsed
		a.subs.chat.AddThinkingBlock("", expanded)
	}
	a.stream.text.WriteString(ev.Text)
	a.subs.chat.UpdateLastMessage(a.stream.text.String(), tui.ConsoleThinkingBlock)
	a.subs.statusMsg.Show("Thinking...")
}

func (a *App) handleAssistantContent(ev *agentic.OutputEvent) {
	a.endStreamIfDifferent(agentic.StateContent)
	// Ensure the activity spinner is visible — the model may emit EventContent
	// without a preceding EventStateChange (e.g., subsequent turns after the first).
	// Show() is idempotent: if already spinning with same text, it returns early.
	a.setStreamingStatus()
	if !a.stream.is(tui.ConsoleAssistantMessage) {
		a.stream.begin(tui.ConsoleAssistantMessage)
		a.subs.chat.AddAssistantMessage("")
	}
	a.stream.text.WriteString(ev.Text)
	a.subs.chat.UpdateLastMessage(a.stream.text.String(), tui.ConsoleAssistantMessage)
}

// endCurrentStream stops any active streaming segment so the next content
// event of a different type starts a new block.
func (a *App) endCurrentStream() {
	a.stream.end()
}

// endStreamIfDifferent ends the current streaming block when the new agent
// state corresponds to a different block type. This prevents a thinking block
// from being reused for later assistant content (or vice-versa) after a state
// transition or tool call.
func (a *App) endStreamIfDifferent(state agentic.OutputState) {
	if !a.stream.active() {
		return
	}
	switch state {
	case agentic.StateThinking:
		if a.stream.kind != tui.ConsoleThinkingBlock {
			a.endCurrentStream()
		}
	case agentic.StateContent:
		if a.stream.kind != tui.ConsoleAssistantMessage {
			a.endCurrentStream()
		}
	case agentic.StateToolCall, agentic.StateToolResult, agentic.StateIdle:
		a.endCurrentStream()
	}
}

func (a *App) handleToolResult(ev *agentic.OutputEvent) {
	// Ensure any leftover stream is closed before processing a tool result.
	a.endStreamIfDifferent(agentic.StateToolResult)

	// Normalize: the dedicated ToolResult field is an alias for Text. The
	// agent emits results in Text; tolerate emitters/tests using ToolResult.
	if ev.Text == "" && ev.ToolResult != "" {
		ev.Text = ev.ToolResult
	}

	a.statsMu.Lock()
	a.toolResultsSeen++
	// Track tool call warning level for color-coding the TC:N footer display.
	// Synthetic budget/repeat results start with "[goa-system]"; detect the
	// severity from the message content.
	if strings.HasPrefix(ev.Text, "[goa-system]") {
		switch {
		case strings.Contains(ev.Text, "budget exceeded"):
			a.toolCallWarningLevel = maxToolCallLevel(a.toolCallWarningLevel, ToolCallStopped)
		case strings.Contains(ev.Text, "Loop guardrail"),
			strings.Contains(ev.Text, "identical to the previous"):
			a.toolCallWarningLevel = maxToolCallLevel(a.toolCallWarningLevel, ToolCallWarning)
			// All other [goa-system] messages are informational (repeated call hint,
			// round limit reached, truncated result) — keep ToolCallNormal.
		}
	}
	a.statsMu.Unlock()

	// Restore terminal title when a bash command completes
	if a.subs.tuiEngine != nil {
		cwdBase := ""
		if a.subs.projectDir != "" {
			cwdBase = filepath.Base(a.subs.projectDir)
		}
		a.setBaseTitle(titleBrand + " - " + cwdBase)
	}

	if tc := a.toolTracker().OnResult(ev); tc != nil {
		a.echoScrolledOffToolResult(tc, ev)
		a.clearToolBusy()
		return
	}
	// No tracked widget matched (e.g. a result for a call whose widget was
	// already retired or never seen): render a plain tool-result entry.
	a.subs.chat.AddToolResult(ev.Text)
	a.clearToolBusy()
}

// echoScrolledOffToolResult appends a compact completion echo when a tool
// finishes while its widget is fully scrolled into terminal scrollback. The
// compositor never repaints committed rows, so the widget's ✓/✗ transition
// would be invisible and the frozen running rows would read as "still
// ongoing" (Issue 6: the first series of a parallel cancel batch
// "stayed blue").
//
// The echo is the widget's own one-line CompletionEcho — status icon + call
// identity + timing/output stats (+ the renderer's one-line outcome when it
// implements ResultSummarizer). It deliberately NEVER replays raw output
// lines: echoing the truncated body preview duplicated on-screen content and
// leaked the "… N earlier lines (ctrl+o to expand)" hint into the transcript,
// reading as rendering corruption (the reported offscreen-tool bug).
func (a *App) echoScrolledOffToolResult(tc *tui.ToolExecutionComponent, _ *agentic.OutputEvent) {
	if a.subs.chat == nil || !a.subs.chat.IsScrolledOff(tc) {
		return
	}
	// Boxed continuation of the tool block: status-colored one-liner keeping
	// the ← continuation marker (bugs.md 2026-08-26).
	a.subs.chat.AddToolEcho(tc.CompletionEcho(), tc.Status() == tui.ToolSuccess)
}

// handleToolStart flips a tool widget from waiting (⧖, queued) to running
// (elapsed) at the TRUE execution start: the scheduler started the task
// (EventToolStart). Until this arrives a finalized call stays Pending and
// shows "waiting Ns…" — never a fake elapsed that includes queue time
// (Bug W).
func (a *App) handleToolStart(ev *agentic.OutputEvent) {
	a.toolTracker().OnStart(ev)
}

// handleToolProgress renders partial output emitted by a still-running tool
// (EventToolProgress, e.g. streamed bash stdout) into its widget without
// completing it. The widget stays in the Running state with its live elapsed
// timer; only the displayed output is refreshed so the user sees progress
// instead of a frozen spinner. The tracker resolves the widget without
// retiring it (so the eventual EventToolResult still resolves).
func (a *App) handleToolProgress(ev *agentic.OutputEvent) {
	if tc := a.toolTracker().OnProgress(ev); tc != nil {
		return
	}
}

// failPendingTools marks every tool widget the tracker still considers
// in-flight as interrupted (✗). This is the safety net at EventEnd for tools
// cancelled mid-run; with the tracker it should rarely fire.
// setStreamingStatus shows the most informative status label for the current
// phase. When a tool batch is in progress it shows "Tool calling (X/Y)",
// otherwise "Answering...".
func (a *App) setStreamingStatus() {
	if a.subs.agentMgr != nil {
		if agent := a.subs.agentMgr.CurrentAgent(); agent != nil && agent.BufferedToolCallCount() > 0 {
			a.subs.statusMsg.Show(a.toolCallProgressLabel())
			return
		}
	}
	a.subs.statusMsg.Show("Answering...")
}

// failPendingTools walks all tool widgets in the chat viewport and marks any
// that are still in Running or Pending state as interrupted (ToolError).
// This catches stragglers from EVERY path (foreground tracker, orchestrator
// agent streams, or any orphan) so cancelled tools show ✗ instead of hanging.
// A widget whose arguments never finished streaming was canceled BEFORE the
// tool executed — it is labeled accordingly so the user does not think work
// happened and its output was lost ("Tool call start a review but
// no output of work done").
// The foreground tracker is reset so the next turn starts clean.
func (a *App) failPendingTools() {
	a.subs.toolTracker = nil
	if a.subs.chat == nil {
		return
	}
	interrupted := 0
	for _, c := range a.subs.chat.Children() {
		tc, ok := c.(*tui.ToolExecutionComponent)
		if !ok {
			continue
		}
		if tc.Status() == tui.ToolPending || tc.Status() == tui.ToolRunning {
			if !tc.ArgsComplete() {
				tc.SetOutput("(canceled before execution — the tool never ran)")
			} else {
				tc.SetOutput("(interrupted)")
			}
			tc.SetStatus(tui.ToolError)
			tc.SetPartial(false)
			interrupted++
		}
	}
	if interrupted > 0 && a.subs.tuiEngine != nil {
		a.subs.tuiEngine.RequestRender()
	}
}

func (a *App) handleSessionEnd(ev *agentic.OutputEvent) {
	streamKind := a.stream.kind
	hadActiveStream := a.stream.active()
	a.endCurrentStream()
	a.stream = streamState{} // full reset

	// Mark any tool widgets still in Running/Pending state as interrupted.
	// Without this, tools interrupted by cancellation or error would stay
	// in "⟳ running" state forever, giving no visible indication of failure.
	a.failPendingTools()

	a.statsMu.Lock()
	a.sessionActive = false
	a.toolResultsSeen = 0
	a.turnCount++
	a.toolCallWarningLevel = ToolCallNormal // reset per-turn so TC color doesn't persist across turns
	stats := a.buildFooterStatsLocked()
	a.statsMu.Unlock()

	subs := a.subs
	subs.toolTracker = nil // fresh tracker for the next turn

	// Check if the event carries an error (agentmanager.go emits EventEnd with
	// non-empty Text on stream/connection errors). Surface it to the user with
	// a clear explanation and actionable hint. User-initiated cancellation is
	// marked with Metadata["cancelled"] and is treated as a graceful stop.
	if ev != nil && ev.Text != "" {
		hint := friendlyConnectionHint(ev.Text)
		subs.chat.AddSystemMessage(hint)
	} else if ev != nil && ev.Metadata["cancelled"] == "true" {
		if hadActiveStream {
			subs.chat.RemoveLastMessageOfType(streamKind)
		}
		subs.chat.AddSystemMessage("Generation stopped by user.")
	}

	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  activeModelDisplay(subs),
		Profile:                string(subs.effectiveModeState().Major),
		Mode:                   string(subs.effectiveModeState().Autonomy),
		Stats:                  formatFooterStats(stats),
		Activity:               "",
		MainActivity:           "",
		CompanionModel:         companionModelDisplay(subs),
		Provider:               sessionProviderID(subs),
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
	subs.footer.SetModelBusy(false)
	subs.statusMsg.SessionEnd()
	if subs.tuiEngine != nil {
		subs.tuiEngine.RequestRender()
	}

	// Log turn stats
	a.logTurnStats(ev)
}

// logTurnStats writes a structured stats line to the agent log on each EventEnd.
func (a *App) logTurnStats(ev *agentic.OutputEvent) {
	if a.subs.logger == nil {
		return
	}
	a.statsMu.Lock()
	modelCfg := a.subs.cfg.GetModelByID(a.subs.cfg.ActiveModel)
	ctxPct := 0.0
	if a.tokenSessionMax > 0 {
		// The log's context figure is the projected occupancy (CX8/P20), the
		// same provider-anchored figure the footer shows.
		ctxTokens := a.tokenSessionProjected
		if ctxTokens <= 0 {
			ctxTokens = a.tokenSessionEstimate
		}
		ctxPct = float64(ctxTokens) / float64(a.tokenSessionMax) * 100
	}
	turn := a.turnCount
	promptN := a.lastTurnPromptN
	predictedN := a.lastTurnPredictedN
	speed := a.lastTurnSpeed
	ctxMax := a.tokenSessionMax
	tokenTotalPrompt := a.tokenPromptTotal
	tokenTotalPredicted := a.tokenPredictedTotal
	tokenCacheRead := a.tokenCacheReadTotal
	tokenCacheWrite := a.tokenCacheWriteTotal
	cacheMissesFull := a.tokenCacheFullMisses
	cacheMissesPartial := a.tokenCachePartialMisses
	statsSeen := a.turnStatsSeen
	a.turnStatsSeen = false // next turn starts with no stats observed
	a.statsMu.Unlock()

	// A turn that never reached the LLM (guardrail latch, connection error,
	// early rejection) emits no EventTokenStats; re-logging the previous
	// turn's stale numbers produced byte-identical [stats] lines across
	// consecutive turns that looked like impossible zero-progress repeats
	// (runaway-loop identical-stats anomaly). Say what happened.
	if !statsSeen {
		a.subs.logger.Log(agentic.Info, fmt.Sprintf("[stats] turn %d: no LLM call (no token stats this turn)", turn))
		return
	}

	line := fmt.Sprintf("[stats] turn %d: in=%d out=%d speed=%.1f ctx=%.1f%%/%d",
		turn, promptN, predictedN, speed, ctxPct, ctxMax)
	if cacheMissesFull > 0 || cacheMissesPartial > 0 {
		line += fmt.Sprintf(" cm=%d|%d", cacheMissesFull, cacheMissesPartial)
	}

	if modelCfg != nil && modelCfg.Pricing != nil {
		cost := computeCost(tokenTotalPrompt, tokenTotalPredicted, tokenCacheRead, tokenCacheWrite, modelCfg.Pricing)
		line += fmt.Sprintf(" cost=$%.4f", cost)
	}

	a.subs.logger.Log(agentic.Info, line)
}

func (a *App) handleStateChange(ev *agentic.OutputEvent) {
	// A spurious mid-turn EventEnd arms the status spinner's session-ended
	// guard, which would silently drop every subsequent Show() and leave the
	// spinner dark for the rest of the turn. A transition to an active state
	// proves the turn is still alive, so reset the guard before updating the
	// status label.
	if ev.State != agentic.StateIdle {
		a.subs.statusMsg.Reset()
	}

	// Break any active streaming block when the agent moves to a different
	// output state, so thinking/content/tool segments stay in separate blocks.
	a.endStreamIfDifferent(ev.State)

	activity := ""
	mainActivity := ""
	switch ev.State {
	case agentic.StateThinking:
		activity = "thinking"
		mainActivity = "thinking"
		a.subs.statusMsg.Show("Thinking...")
	case agentic.StateContent:
		activity = "streaming"
		mainActivity = "streaming"
		a.subs.statusMsg.Show("Answering...")
	case agentic.StateToolCall:
		activity = "tool calling"
		// Keep the chat spinner busy, but the footer model spinner is not
		// the model generating — it is a tool running. The tool progress is
		// shown in the chat status spinner instead.
		mainActivity = ""
		a.subs.statusMsg.Show(a.toolCallProgressLabel())
	case agentic.StateToolResult:
		// The harness is sending tool results back to the LLM. Keep the
		// spinner active with the most accurate label: tool progress if
		// more calls are still pending, otherwise "Sending request...".
		activity = ""
		mainActivity = ""
		if a.subs.agentMgr != nil {
			if agent := a.subs.agentMgr.CurrentAgent(); agent != nil && agent.BufferedToolCallCount() > 0 {
				a.subs.statusMsg.Show(a.toolCallProgressLabel())
				break
			}
		}
		a.subs.statusMsg.Show("Sending request...")
	}
	subs := a.subs
	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  activeModelDisplay(subs),
		Profile:                string(subs.effectiveModeState().Major),
		Mode:                   string(subs.effectiveModeState().Autonomy),
		Activity:               activity,
		MainActivity:           mainActivity,
		CompanionModel:         companionModelDisplay(subs),
		Provider:               sessionProviderID(subs),
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
	// Only the main model spinner for actual model generation states;
	// tool calls are surfaced by the chat status spinner, not the model
	// spinner in the footer.
	subs.footer.SetModelBusy(mainActivity != "")
}

// toolTracker returns the foreground conversation's tool-call tracker,
// lazily binding it to the chat viewport. All tool widgets for the main
// agent are created exclusively through it, which guarantees exactly one
// widget per logical tool call (late-id adoption) and prevents the
// "stuck on write" orphan bug.
func (a *App) toolTracker() *tooltracker.Tracker {
	if a.subs.toolTracker == nil {
		chat := a.subs.chat
		a.subs.toolTracker = tooltracker.New(func(name, input string) *tui.ToolExecutionComponent {
			if chat == nil {
				return nil
			}
			return chat.AddToolExecution(name, input)
		})
	}
	return a.subs.toolTracker
}

func (a *App) handleToolCall(ev *agentic.OutputEvent) {
	oldText := a.subs.statusMsg.Text()
	// Finalize any active thinking/content stream before rendering a tool call.
	a.endStreamIfDifferent(agentic.StateToolCall)

	tc, created := a.toolTracker().OnCall(ev)
	if tc == nil {
		return
	}

	// Only the first appearance of a tool call counts toward the session
	// total; streaming deltas and late-id adoptions reuse the existing widget.
	if created {
		a.statsMu.Lock()
		a.toolCallsTotal++
		a.statsMu.Unlock()
	}

	label := a.toolCallProgressLabel()
	if ev.IsDelta {
		// Streaming partial: keep a descriptive label until the call completes.
		label = "Calling " + ev.ToolName + "..."
	}
	// E5 (ENHANCE.md): collapse identical re-emissions. A streaming tool call
	// fires one EventToolCall per delta, each re-entering here with the same
	// label; the status spinner dedupes internally, but the footer update and
	// the status log still ran per delta (2026-08-05 export: 13 identical
	// "Calling bash..." log lines in 0.4s). When the label is unchanged and
	// the call is not newly created, the emission is a no-op: skip it.
	unchanged := !created && label == oldText
	if unchanged {
		return
	}
	// Start the shared status spinner first so that the tool widget and
	// footer observe a non-empty CurrentSpinnerFrame when they render.
	a.subs.statusMsg.Show(label)
	// NOTE: the widget is deliberately NOT stamped Running here. Stamping at
	// args-complete starts every widget of a batch at the same instant, so
	// queued (conflict-serialized) calls display a fake ticking "elapsed"
	// (Multi-tool calling and timeout+ Bug W). The Running
	// transition happens on EventToolStart (true scheduler execution start)
	// or, as a backstop, the call's first progress event.

	// The footer model spinner is not used during a tool call; only the chat
	// status spinner shows the tool's progress.
	a.setToolCallingFooter(label)
	a.setBashTitle(ev.ToolName, ev.ToolInput)

	if a.subs.logger != nil {
		a.subs.logger.Log(agentic.Info, "[status] handleToolCall: tool=%s oldText=%q newText=%q visible=%v",
			ev.ToolName, oldText, a.subs.statusMsg.Text(), a.subs.statusMsg.IsVisible())
	}
}

// (handleStreamingToolCallUpdate / findActiveToolWidget / createStreamingToolWidget
// were folded into the ToolCallTracker, which owns widget identity for both
// delta and final tool-call events.)

func (a *App) setWaitingForReplyStatus(pp *agentic.PromptProgress) {
	subs := a.subs
	label := "Sending request..."
	if pp.Total > 0 {
		pct := pp.Processed * 100 / pp.Total
		label = fmt.Sprintf("Processing... %d%%", pct)
	}
	subs.statusMsg.Show(label)
	if pp.Total > 0 {
		subs.footer.SetData(tui.FooterData{
			Workdir:                subs.projectDir,
			Model:                  activeModelDisplay(subs),
			Profile:                string(subs.effectiveModeState().Major),
			Mode:                   string(subs.effectiveModeState().Autonomy),
			Activity:               "wait",
			MainActivity:           label,
			CompanionModel:         companionModelDisplay(subs),
			Provider:               sessionProviderID(subs),
			ThinkingLevel:          mainThinkingLevel(subs),
			CompanionThinkingLevel: companionThinkingLevel(subs),
		})
		subs.footer.SetModelBusy(true)
	}
	subs.tuiEngine.RequestRender()
}

// cacheBustDropToleranceTokens is the wobble absorbed before a drop in cache
// reads counts as a cache bust: providers report cached tokens at block
// granularity (e.g. 256-token blocks on kimi), so tiny dips between requests
// are reporting noise, not invalidation. Real busts (compaction truncation,
// TTL expiry) collapse reads by orders of magnitude more.
