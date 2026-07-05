// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/pijalu/goa/config"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/ansi"
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

// maxToolCallLevel returns the maximum of two ToolCallLevel values.
func maxToolCallLevel(a, b ToolCallLevel) ToolCallLevel {
	if a > b {
		return a
	}
	return b
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

	if tc := a.lookupActiveTool(ev.ToolCallID); tc != nil {
		a.applyToolResultToWidget(tc, ev)
		return
	}
	// Fallback: if the tool result did not carry a matching ID, update the
	// oldest still-pending tool widget so the result is visible in the widget
	// instead of appearing as a separate text entry.
	if tc := a.findPendingTool(); tc != nil {
		a.applyToolResultToWidget(tc, ev)
		return
	}
	a.subs.chat.AddToolResult(ev.Text)
	a.clearToolBusy()
}

// applyToolResultToWidget updates a single tool widget with the result text,
// status, and final (non-partial) marker.
func (a *App) applyToolResultToWidget(tc *tui.ToolExecutionComponent, ev *agentic.OutputEvent) {
	tc.SetOutput(ev.Text)
	tc.SetStatus(a.toolStatusFromResult(ev.Text))
	tc.SetPartial(false)
	a.clearToolBusy()
}

// lookupActiveTool returns the in-flight tool component matching the given
// ToolCallID. The matched entry is removed so subsequent results do not
// overwrite it. Non-empty IDs are only matched against the ID map; they do not
// fall back to the legacy single-slot, which would update the wrong widget
// when multiple tools are in flight.
func (a *App) lookupActiveTool(callID string) *tui.ToolExecutionComponent {
	if callID != "" {
		if tc, ok := a.subs.activeTools[callID]; ok {
			delete(a.subs.activeTools, callID)
			return tc
		}
		return nil
	}
	if a.subs.activeTool != nil {
		tc := a.subs.activeTool
		a.subs.activeTool = nil
		return tc
	}
	return nil
}

// findPendingTool walks the chat entries from oldest to newest and returns the
// first tool widget that has not yet been marked as success or error. This is
// a best-effort fallback when the provider does not include ToolCallIDs.
func (a *App) findPendingTool() *tui.ToolExecutionComponent {
	for _, c := range a.subs.chat.Children() {
		tc, ok := c.(*tui.ToolExecutionComponent)
		if !ok {
			continue
		}
		if tc.Status() != tui.ToolSuccess && tc.Status() != tui.ToolError {
			return tc
		}
	}
	return nil
}

func (a *App) clearToolBusy() {
	// After a tool result the harness sends the updated context back to the
	// LLM. Show "Sending request..." so the UI does not prematurely report
	// "Answering..." while the model is still being prepared.
	a.subs.statusMsg.Show("Sending request...")
	a.subs.footer.SetModelBusy(false)
}

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
// This ensures that tools interrupted by session cancellation or errors show
// as ✗ (error) rather than remaining in "⟳ running" state indefinitely.
func (a *App) failPendingTools() {
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
			tc.SetOutput("(interrupted)")
			tc.SetStatus(tui.ToolError)
			tc.SetPartial(false)
			interrupted++
		}
	}
	if interrupted > 0 && a.subs.tuiEngine != nil {
		a.subs.tuiEngine.RequestRender()
	}
}

func (a *App) toolStatusFromResult(text string) tui.ToolStatus {
	trimmed := strings.TrimSpace(text)
	// Budget-exceeded calls did not actually run; surface them as errors (✗)
	// so the user is not misled into thinking the tool succeeded.
	if strings.HasPrefix(trimmed, agentic.ToolBudgetResultPrefix) {
		return tui.ToolError
	}
	if strings.HasPrefix(trimmed, "Error:") {
		return tui.ToolError
	}
	return tui.ToolSuccess
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
	subs.activeTool = nil
	subs.activeTools = nil

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
		mainActivity = a.toolCallProgressLabel()
		a.subs.statusMsg.Show(mainActivity)
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
	subs.footer.SetModelBusy(true)
}

// toolCallProgressLabel returns "Tool calling (X/Y)" when we know how many
// calls are in the current batch and how many have been seen so far.
func (a *App) toolCallProgressLabel() string {
	total := 0
	seen := 0
	if a.subs.agentMgr != nil {
		if agent := a.subs.agentMgr.CurrentAgent(); agent != nil {
			total = agent.BufferedToolCallCount()
		}
	}
	a.statsMu.Lock()
	seen = a.toolResultsSeen
	a.statsMu.Unlock()

	if total > 1 {
		return fmt.Sprintf("Tool calling (%d/%d)", min(seen+1, total), total)
	}
	if total == 1 {
		return "Tool calling (1/1)"
	}
	return "Tool calling"
}

func (a *App) handleToolCall(ev *agentic.OutputEvent) {
	oldText := a.subs.statusMsg.Text()
	// Finalize any active thinking/content stream before rendering a tool call.
	// The streaming path emits EventToolCall without a preceding state change,
	// so the stream state machine must be broken here.
	a.endStreamIfDifferent(agentic.StateToolCall)

	a.statsMu.Lock()
	a.toolCallsTotal++
	a.statsMu.Unlock()

	tc := a.subs.chat.AddToolExecution(ev.ToolName, ev.ToolInput)
	if ev.ToolCallID != "" {
		if a.subs.activeTools == nil {
			a.subs.activeTools = make(map[string]*tui.ToolExecutionComponent)
		}
		a.subs.activeTools[ev.ToolCallID] = tc
	} else {
		a.subs.activeTool = tc
	}

	label := a.toolCallProgressLabel()
	// Start the shared status spinner first so that the tool widget and
	// footer observe a non-empty CurrentSpinnerFrame when they render.
	a.subs.statusMsg.Show(label)
	tc.SetStatus(tui.ToolRunning)

	// Keep the footer busy indicator spinning during the tool call. The
	// streaming path may emit EventToolCall without a preceding state change,
	// so the footer must be updated here as well as in handleStateChange.
	subs := a.subs
	subs.footer.SetData(tui.FooterData{
		Workdir:                subs.projectDir,
		Model:                  activeModelDisplay(subs),
		Profile:                string(subs.effectiveModeState().Major),
		Mode:                   string(subs.effectiveModeState().Autonomy),
		Activity:               "tool calling",
		MainActivity:           label,
		CompanionModel:         companionModelDisplay(subs),
		Provider:               subs.cfg.ActiveProvider,
		ThinkingLevel:          mainThinkingLevel(subs),
		CompanionThinkingLevel: companionThinkingLevel(subs),
	})
	subs.footer.SetModelBusy(true)

	// Update terminal title for bash commands
	if ev.ToolName == "bash" && a.subs.tuiEngine != nil {
		var params struct {
			Command string `json:"command"`
		}
		if err := json.Unmarshal([]byte(ev.ToolInput), &params); err == nil && params.Command != "" {
			cmd := params.Command
			if len(cmd) > 60 {
				cmd = cmd[:57] + "..."
			}
			a.subs.tuiEngine.SetTitle("goa - $ " + cmd)
		}
	}

	if a.subs.logger != nil {
		a.subs.logger.Log(agentic.Info, "[status] handleToolCall: tool=%s oldText=%q newText=%q visible=%v",
			ev.ToolName, oldText, a.subs.statusMsg.Text(), a.subs.statusMsg.IsVisible())
	}
}

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
		pct := computeCacheHitPct(s.CacheReadTotal, s.CacheWriteTotal, s.PromptN)
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

// computeCacheHitPct calculates the cache hit percentage.
// When CacheWrite > 0, the rate is reads / (reads + writes), measuring what
// fraction of cache operations were hits. When CacheWrite is 0 (OpenAI-style),
// the denominator is reads + net prompt tokens (non-cached portion), providing
// a meaningful rate instead of always showing 100%.
func computeCacheHitPct(cacheRead, cacheWrite, promptN int) float64 {
	if cacheWrite > 0 {
		denom := cacheRead + cacheWrite
		if denom == 0 {
			denom = 1
		}
		return float64(cacheRead) / float64(denom) * 100
	}
	denom := cacheRead + promptN
	if denom == 0 {
		denom = 1
	}
	return float64(cacheRead) / float64(denom) * 100
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
