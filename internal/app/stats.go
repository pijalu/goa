// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/metrics"
	"github.com/pijalu/goa/internal/tooltracker"
	"github.com/pijalu/goa/tui"
)

// streamState tracks the current streaming context for LLM output.
// Decoupled from content type so thinking segments break correctly on
// any non-thinking event (tool call, tool result, content, idle, end).
type streamState struct {
	kind     tui.ConsoleItemType
	text     strings.Builder
	isActive bool
}

func (s *streamState) begin(kind tui.ConsoleItemType) {
	s.kind = kind
	s.text.Reset()
	s.isActive = true
}

func (s *streamState) end() {
	s.isActive = false
	s.text.Reset()
}

func (s *streamState) is(kind tui.ConsoleItemType) bool {
	return s.isActive && s.kind == kind
}

func (s *streamState) active() bool {
	return s.isActive
}

// ToolCallLevel indicates the severity of tool call loop detection for
// color-coding the TC:N display in the footer.
type ToolCallLevel int

const (
	ToolCallNormal  ToolCallLevel = 0 // green — all good
	ToolCallWarning ToolCallLevel = 1 // orange — duplicate/repeat detected
	ToolCallStopped ToolCallLevel = 2 // red — budget exceeded, force-stopped
)

// sessionStats holds cumulative + last-turn statistics for footer display.
type sessionStats struct {
	PromptN         int
	PredictedN      int
	CacheReadTotal  int
	CacheWriteTotal int
	SpeedTokPerSec  float64 // last turn output tok/s
	ContextEstimate int
	ContextMax      int
	ContextAutoMax  bool // true when ContextMax was inferred from model metadata
	CostUSD         float64
	ShowCost        bool
	ToolCalls       int
	ToolCallLevel   ToolCallLevel // 0=normal, 1=warning, 2=stopped
	MicroCompacts   int
	Compacts        int
	PrevCacheHitPct float64 // previous cache hit % for evolution comparison
}

func (a *App) handleAgentOutputEvent(ev *agentic.OutputEvent) {
	switch ev.Type {
	case agentic.EventContent:
		a.handleStreamContent(ev)
	case agentic.EventToolResult:
		a.handleToolResult(ev)
	case agentic.EventEnd:
		a.handleSessionEnd(ev)
	case agentic.EventStateChange:
		a.handleStateChange(ev)
	case agentic.EventToolCall:
		a.handleToolCall(ev)
	case agentic.EventToolProgress:
		a.handleToolProgress(ev)
	case agentic.EventTokenStats, agentic.EventContextStats:
		a.handleTokenStats(ev)
	case agentic.EventCompact:
		a.recordCompact(ev.Text)
	case agentic.EventClear:
		a.clearStats()
		a.handleTokenStats(ev)
	case agentic.EventProgress:
		a.handleProgressEvent(ev)
	default:
		a.handleTokenStats(ev)
	}
}

func (a *App) recordCompact(kind string) {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	if kind == "micro" {
		a.microCompacts++
	} else {
		a.compacts++
	}
}

func (a *App) clearStats() {
	a.statsMu.Lock()
	defer a.statsMu.Unlock()
	a.tokenPromptTotal = 0
	a.tokenPredictedTotal = 0
	a.tokenCacheReadTotal = 0
	a.tokenCacheWriteTotal = 0
	a.lastTurnPromptN = 0
	a.lastTurnPredictedN = 0
	a.lastTurnCacheRead = 0
	a.lastTurnCacheWrite = 0
	a.tokenSessionMax = 0
	a.tokenSessionMaxAuto = false
	a.tokenSessionEstimate = 0
	a.lastTurnSpeed = 0
	a.turnCount = 0
	a.microCompacts = 0
	a.compacts = 0
	a.toolCallsTotal = 0
	a.toolCallWarningLevel = ToolCallNormal
	a.prevCacheHitPct = 0
}

func (a *App) handleProgressEvent(ev *agentic.OutputEvent) {
	// Show prompt-processing progress while waiting for the first token,
	// or reconnection/status messages.
	if ev.PromptProgress != nil {
		a.setWaitingForReplyStatus(ev.PromptProgress)
	}
	if ev.Text != "" {
		a.subs.statusMsg.Show(ev.Text)
		a.subs.tuiEngine.RequestRender()
	}
}

func (a *App) handleStreamContent(ev *agentic.OutputEvent) {
	if ev.Role == agentic.User || ev.Role == agentic.System {
		if ev.Role == agentic.User && ev.Text != "" && isReplay(ev) {
			a.subs.chat.AddUserMessage(ev.Text)
		}
		if ev.Role == agentic.System && ev.Text != "" && isSystemNotification(ev) {
			a.endCurrentStream()
			a.subs.chat.AddSystemMessage(ev.Text)
		}
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
		a.subs.tuiEngine.SetTitle("goa - " + cwdBase)
	}

	if tc := a.toolTracker().OnResult(ev); tc != nil {
		a.clearToolBusy()
		return
	}
	// No tracked widget matched (e.g. a result for a call whose widget was
	// already retired or never seen): render a plain tool-result entry.
	a.subs.chat.AddToolResult(ev.Text)
	a.clearToolBusy()
}

// applyToolResultToWidget is retained for the orchestrator/multi-agent path
// and any caller that applies a result directly to a known widget. The
// foreground path delegates result application to the ToolCallTracker.
func (a *App) applyToolResultToWidget(tc *tui.ToolExecutionComponent, ev *agentic.OutputEvent) {
	tc.SetOutput(ev.Text)
	tc.SetStatus(a.toolStatusFromResult(ev.Text))
	tc.SetPartial(false)
	a.clearToolBusy()
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
// happened and its output was lost (bugs.md: "Tool call start a review but
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
		Provider:               subs.cfg.ActiveProvider,
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
		ctxPct = float64(a.tokenSessionEstimate) / float64(a.tokenSessionMax) * 100
	}
	turn := a.turnCount
	promptN := a.lastTurnPromptN
	predictedN := a.lastTurnPredictedN
	speed := a.lastTurnSpeed
	ctxMax := a.tokenSessionMax
	tokenTotalPrompt := a.tokenPromptTotal
	tokenTotalPredicted := a.tokenPredictedTotal
	a.statsMu.Unlock()

	line := fmt.Sprintf("[stats] turn %d: in=%d out=%d speed=%.1f ctx=%.1f%%/%d",
		turn, promptN, predictedN, speed, ctxPct, ctxMax)

	if modelCfg != nil && modelCfg.Pricing != nil {
		cost := computeCost(tokenTotalPrompt, tokenTotalPredicted, modelCfg.Pricing)
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
		Provider:               subs.cfg.ActiveProvider,
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
	// Start the shared status spinner first so that the tool widget and
	// footer observe a non-empty CurrentSpinnerFrame when they render.
	a.subs.statusMsg.Show(label)
	if !ev.IsDelta {
		tc.SetStatus(tui.ToolRunning)
	}

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
			Provider:               subs.cfg.ActiveProvider,
			ThinkingLevel:          mainThinkingLevel(subs),
			CompanionThinkingLevel: companionThinkingLevel(subs),
		})
		subs.footer.SetModelBusy(true)
	}
	subs.tuiEngine.RequestRender()
}

func (a *App) handleTokenStats(ev *agentic.OutputEvent) {
	a.statsMu.Lock()
	// Extract token counts from timings
	if ev.Timings != nil {
		a.lastTurnPromptN = ev.Timings.PromptN
		a.lastTurnPredictedN = ev.Timings.PredictedN
		a.tokenPromptTotal += ev.Timings.PromptN
		a.tokenPredictedTotal += ev.Timings.PredictedN

		// Track cache tokens
		a.lastTurnCacheRead = ev.Timings.CacheReadTokens
		a.lastTurnCacheWrite = ev.Timings.CacheWriteTokens
		a.tokenCacheReadTotal += ev.Timings.CacheReadTokens
		a.tokenCacheWriteTotal += ev.Timings.CacheWriteTokens

		// Capture last-turn output speed
		a.lastTurnSpeed = ev.Timings.PredictedPerSecond
		if a.lastTurnSpeed == 0 && ev.Timings.PredictedMs > 0 {
			a.lastTurnSpeed = float64(ev.Timings.PredictedN) / (ev.Timings.PredictedMs / 1000.0)
		}
	}

	// Extract context window usage
	if ev.ContextStats != nil {
		a.tokenSessionMax = ev.ContextStats.MaxTokens
		a.tokenSessionMaxAuto = ev.ContextStats.AutoMax
		a.tokenSessionEstimate = ev.ContextStats.EstimatedTokens
	}

	// Compute cost from active model's pricing config
	stats := a.buildFooterStatsLocked()
	a.statsMu.Unlock()

	subs := a.subs
	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  activeModelDisplay(subs),
		Profile:                string(subs.effectiveModeState().Major),
		Mode:                   string(subs.effectiveModeState().Autonomy),
		Stats:                  formatFooterStats(stats),
		CompanionModel:         companionModelDisplay(subs),
		Provider:               subs.cfg.ActiveProvider,
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
}

// buildFooterStatsLocked requires a.statsMu to be held by the caller.
func (a *App) buildFooterStatsLocked() sessionStats {
	st := sessionStats{
		PromptN:         a.tokenPromptTotal,
		PredictedN:      a.tokenPredictedTotal,
		CacheReadTotal:  a.tokenCacheReadTotal,
		CacheWriteTotal: a.tokenCacheWriteTotal,
		SpeedTokPerSec:  a.lastTurnSpeed,
		ContextEstimate: a.tokenSessionEstimate,
		ContextMax:      a.tokenSessionMax,
		ContextAutoMax:  a.tokenSessionMaxAuto,
		ToolCalls:       a.toolCallsTotal,
		ToolCallLevel:   a.toolCallWarningLevel,
	}
	applyPricing(&st, a.subs.cfg, a.subs.cfg.ActiveModel)
	st.MicroCompacts = a.microCompacts
	st.Compacts = a.compacts
	return st
}

// applyPricing computes cost and pricing-related visibility flags for the
// given session stats using the model identified by activeModelID.
func applyPricing(st *sessionStats, cfg *config.Config, activeModelID string) {
	modelCfg := cfg.GetModelByID(activeModelID)
	if modelCfg == nil || modelCfg.Pricing == nil {
		return
	}
	st.CostUSD = computeCost(st.PromptN, st.PredictedN, modelCfg.Pricing)
	if st.CostUSD > 0 || modelCfg.Pricing.InputPer1M > 0 || modelCfg.Pricing.OutputPer1M > 0 {
		st.ShowCost = true
	}
}

// computeCost computes cumulative cost from token totals and the model's pricing config.
// friendlyConnectionHint translates a raw connection error into a user-friendly
// message with an actionable hint.
func friendlyConnectionHint(raw string) string {
	if raw == "" {
		return ""
	}
	switch {
	case strings.Contains(raw, "SSE stream ended prematurely"),
		strings.Contains(raw, "finish_reason"):
		return "[connection error] The LLM stream ended unexpectedly before the response was complete.\n" +
			"  • This may be a temporary server hiccup — goa will retry automatically\n" +
			"  • If the problem persists, check your LLM server logs and network connection"
	case strings.Contains(raw, "context deadline exceeded"),
		strings.Contains(raw, "timeout"),
		strings.Contains(raw, "Client.Timeout"):
		return "[connection error] The request timed out — the LLM server is taking too long to respond.\n" +
			"  • Check that your local LLM server (LM Studio, llama.cpp, etc.) is running\n" +
			"  • The model may still be loading — wait and try again\n" +
			"  • Try a smaller/faster model if this persists"
	case strings.Contains(raw, "connection refused"),
		strings.Contains(raw, "connect: connection refused"):
		return "[connection error] Could not connect to the LLM server.\n" +
			"  • Make sure the server is running and the URL/port is correct\n" +
			"  • Check your provider configuration with /config"
	case strings.Contains(raw, "no such host"),
		strings.Contains(raw, "lookup"):
		return "[connection error] Could not resolve the LLM server hostname.\n" +
			"  • Check your network connection\n" +
			"  • Verify the provider URL in your configuration"
	case strings.Contains(raw, "401"),
		strings.Contains(raw, "unauthorized"),
		strings.Contains(raw, "invalid API key"):
		return "[connection error] Authentication failed.\n" +
			"  • Check your API key in the provider configuration\n" +
			"  • Run /config to update your credentials"
	default:
		return fmt.Sprintf("[connection error] Connection to the LLM server was lost.\n  %s", raw)
	}
}

func computeCost(promptN, predictedN int, pricing *config.PricingConfig) float64 {
	if pricing == nil {
		return 0
	}
	cost := float64(promptN)/1e6*pricing.InputPer1M +
		float64(predictedN)/1e6*pricing.OutputPer1M
	return cost
}

func formatTokenCount(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 1000000 {
		// Show as K with one decimal
		k := float64(n) / 1000
		return fmt.Sprintf("%.1fK", k)
	}
	m := float64(n) / 1000000
	return fmt.Sprintf("%.1fM", m)
}

func formatFooterStats(s sessionStats) string {
	parts := buildFooterStatParts(s)
	return strings.Join(parts, " ")
}

// formatFooterStatsPlain returns the same textual stats as formatFooterStats
// but with any ANSI escape sequences removed so the output is suitable for
// --plain headless mode or other consumers that must not receive color codes.
func formatFooterStatsPlain(s sessionStats) string {
	parts := buildFooterStatParts(s)
	for i, p := range parts {
		parts[i] = ansi.Strip(p)
	}
	return strings.Join(parts, " ")
}

func buildFooterStatParts(s sessionStats) []string {
	var parts []string
	if s.PromptN > 0 {
		parts = append(parts, "\u2191"+formatTokenCount(s.PromptN))
	}
	if s.PredictedN > 0 {
		parts = append(parts, "\u2193"+formatTokenCount(s.PredictedN))
	}
	if s.SpeedTokPerSec > 0 {
		parts = append(parts, fmt.Sprintf("%.1f tok/s", s.SpeedTokPerSec))
	}
	// Cache hit percentage = CacheRead / (CacheRead + CacheWrite) * 100.
	// This is the standard cache hit rate: what fraction of cache operations
	// were hits vs misses (cache creations). When CacheWrite is 0 (OpenAI-style
	// where cache is a subset of prompt tokens), the rate represents how much
	// of the cache-eligible input was served from cache, using PromptN (net
	// non-cached tokens) as the cache-miss portion.
	if s.CacheReadTotal > 0 || s.CacheWriteTotal > 0 {
		pct := metrics.CacheHitPct(s.CacheReadTotal, s.CacheWriteTotal, s.PromptN)
		parts = append(parts, formatCacheHitPart(pct, s.PrevCacheHitPct))
	}
	if s.ToolCalls > 0 {
		parts = append(parts, formatToolCallPart(s.ToolCalls, s.ToolCallLevel))
	}
	if s.ShowCost && s.CostUSD > 0 {
		parts = append(parts, fmt.Sprintf("$%.4f", s.CostUSD))
	}
	if s.ContextMax > 0 {
		parts = append(parts, formatContextUsage(s.ContextEstimate, s.ContextMax, s.ContextAutoMax))
	}
	// Show compression counters when non-zero.
	if s.MicroCompacts > 0 || s.Compacts > 0 {
		parts = append(parts, fmt.Sprintf("c:%dm-%d", s.MicroCompacts, s.Compacts))
	}
	return parts
}

func formatContextUsage(estimate, max int, autoMax bool) string {
	if max <= 0 {
		return "?"
	}
	pct := float64(estimate) / float64(max) * 100
	value := fmt.Sprintf("%.1f%%/%s", pct, formatTokenCount(max))
	if autoMax {
		value += " (auto)"
	}
	color := tui.TheTheme.ColorHex("status_bar_fg")
	switch {
	case pct > 90:
		color = tui.TheTheme.ColorHex("token_critical")
	case pct > 70:
		color = tui.TheTheme.ColorHex("token_warning")
	}
	return ansi.Fg(color) + value + ansi.Reset
}

// formatCacheHitPart renders the cache hit percentage with color coding
// based on evolution from the previous value:
//   - Growing (>=1%):        light green (#3fb950)
//   - Dropping (1% to <10%): light orange (#d29922)
//   - Dropping (>=10%):      red (#f85149)
//   - Stable (<1% change):   normal status bar color
func formatCacheHitPart(pct, prevPct float64) string {
	delta := pct - prevPct
	colorHex := tui.TheTheme.ColorHex("status_bar_fg")

	switch {
	case delta >= 1.0:
		// Growing cache hit — green
		colorHex = "#3fb950"
	case delta <= -10.0:
		// Dropping significantly — red
		colorHex = "#f85149"
	case delta <= -1.0:
		// Dropping moderately — orange
		colorHex = "#d29922"
	}

	return ansi.Fg(colorHex) + fmt.Sprintf("CH%.1f%%", pct) + ansi.Reset
}

// formatToolCallPart renders the TC:N display with color coding:
//   - green (token_completion):   all good
//   - orange (token_warning):     duplicate/repeat detected
//   - red (token_critical):       budget exceeded, force-stopped
func formatToolCallPart(count int, level ToolCallLevel) string {
	colorHex := tui.TheTheme.ColorHex("status_bar_fg")
	switch level {
	case ToolCallWarning:
		colorHex = tui.TheTheme.ColorHex("token_warning")
	case ToolCallStopped:
		colorHex = tui.TheTheme.ColorHex("token_critical")
	}
	return ansi.Fg(colorHex) + fmt.Sprintf("TC:%d", count) + ansi.Reset
}
