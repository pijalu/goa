// SPDX-License-Identifier: GPL-3.0-or-later

package core

import (
	"strings"

	"github.com/pijalu/goa/internal/agentic"
)

// OnEvent implements agentic.OutputObserver.
func (am *AgentManager) OnEvent(event agentic.OutputEvent) {
	am.logEvent(event)
	am.recordContentEvent(event)
	am.forwardEvent(event)
	am.handleTypedEvent(event)
	am.maybeRefreshContextWindow(event)
}

// maybeRefreshContextWindow triggers a one-time refresh of the local model's
// loaded context window on the first assistant content delta of a session.
// Local servers (LM Studio, llama.cpp) only report the real loaded length once
// the model is actually producing output: querying earlier — e.g. on the
// state-change that merely marks the start of generation — races with model
// loading and yields the configured max_context_length instead of the real
// loaded_context_length. The first response delta is the strongest signal that
// the model is fully loaded, so we defer detection until we see it.
func (am *AgentManager) maybeRefreshContextWindow(event agentic.OutputEvent) {
	if event.Type != agentic.EventContent || event.Role != agentic.Assistant || event.Text == "" {
		return
	}

	am.mu.Lock()
	if am.contextWindowRefreshed || am.contextWindowRefresher == nil || am.activeAgent == nil {
		am.mu.Unlock()
		return
	}
	am.contextWindowRefreshed = true
	refresher := am.contextWindowRefresher
	am.mu.Unlock()

	go func() {
		nCtx := refresher()
		if nCtx <= 0 {
			return
		}
		am.mu.Lock()
		agent := am.activeAgent
		am.mu.Unlock()
		if agent == nil {
			return
		}
		agent.SetContextWindow(nCtx)
		am.emitAgentEvent(agentic.OutputEvent{
			Type: agentic.EventContextStats,
			ContextStats: &agentic.ContextStats{
				MaxTokens: nCtx,
				AutoMax:   true,
			},
		})
	}()
}

func (am *AgentManager) recordContentEvent(event agentic.OutputEvent) {
	if event.Type != agentic.EventContent {
		return
	}
	switch event.Role {
	case agentic.User:
		am.turnRecorder.RecordUserInput(event.Text)
	case agentic.Assistant:
		if event.State == agentic.StateThinking {
			am.turnRecorder.RecordThinkingDelta(event.Text)
			if am.loopDetector != nil {
				lvl := am.loopDetector.RecordThinkingDelta(event.Text)
				am.handleThinkingLoopWarning(lvl)
			}
		} else {
			am.turnRecorder.RecordAssistantDelta(event.Text)
		}
	}
	if am.isCompanionContent(event) {
		am.companionBuf.WriteString(event.Text)
	}
}

func (am *AgentManager) isCompanionContent(event agentic.OutputEvent) bool {
	return event.Type == agentic.EventContent &&
		event.Role == agentic.Assistant &&
		event.Text != "" &&
		event.State != agentic.StateThinking
}

func (am *AgentManager) forwardEvent(event agentic.OutputEvent) {
	// Only write to the internal events channel when a consumer has opted in
	// (headless/ACP). The TUI consumes events from eventsOut.Agent and never
	// reads am.events; writing to an undrained buffered channel would block
	// forever once the 100-slot buffer fills, stalling the agent mid-stream.
	if am.forwardInternalEvents {
		am.events <- event
	}
	// The TUI-bound bus must never drop events; block with backpressure so
	// every delta and EventEnd reaches the renderer.
	am.emitAgentEvent(event)
	if am.sessionStore != nil {
		am.sessionStore.WriteEvent(event)
	}
}

func (am *AgentManager) handleTypedEvent(event agentic.OutputEvent) {
	switch event.Type {
	case agentic.EventToolCall:
		am.handleToolCallEvent(event)
	case agentic.EventToolResult:
		am.handleToolResultEvent(event)
	case agentic.EventTokenStats:
		am.handleTokenStatsEvent(event)
	case agentic.EventContextStats:
		am.handleContextStatsEvent(event)
	case agentic.EventEnd:
		am.finalizeTurn()
	}
}

func (am *AgentManager) handleToolCallEvent(event agentic.OutputEvent) {
	am.turnRecorder.RecordToolCall(event.ToolName, event.ToolInput, event.ToolCallID)
	am.dispatchLifecycle("tool_call", map[string]any{
		"tool":    event.ToolName,
		"input":   event.ToolInput,
		"call_id": event.ToolCallID,
	})
	if am.loopDetector != nil {
		lvl := am.loopDetector.RecordToolCall(event.ToolName, event.ToolInput)
		am.handleLoopWarning(lvl)
	}
	if orch := am.foregroundOrchestrator(); orch != nil {
		orch.ResetCompanionCount()
	}
}

func (am *AgentManager) handleToolResultEvent(event agentic.OutputEvent) {
	am.turnRecorder.RecordToolResult(event.ToolCallID, event.ToolName, event.ToolResult)
	am.dispatchLifecycle("tool_done", map[string]any{
		"tool":    event.ToolName,
		"call_id": event.ToolCallID,
		"result":  event.ToolResult,
	})
	if am.loopDetector != nil {
		am.loopDetector.RecordToolResult(isToolResultError(event.ToolResult))
	}
}

func (am *AgentManager) handleTokenStatsEvent(event agentic.OutputEvent) {
	if event.Timings == nil {
		return
	}
	ctxEstimate, ctxMax := 0, 0
	if event.ContextStats != nil {
		ctxEstimate = event.ContextStats.EstimatedTokens
		ctxMax = event.ContextStats.MaxTokens
	}
	am.turnRecorder.RecordTokenStats(
		event.Timings.PromptN,
		event.Timings.PredictedN,
		event.Timings.CacheReadTokens,
		event.Timings.CacheWriteTokens,
		event.Timings.PredictedPerSecond,
		0, // cost computed at display time
		ctxEstimate, ctxMax,
	)
}

func (am *AgentManager) handleContextStatsEvent(event agentic.OutputEvent) {
	if event.ContextStats == nil {
		return
	}
	// Update only context stats without overwriting token data.
	am.turnRecorder.RecordContextStats(
		event.ContextStats.EstimatedTokens,
		event.ContextStats.MaxTokens,
	)
}

func (am *AgentManager) finalizeTurn() {
	am.mu.Lock()
	agent := am.activeAgent
	am.mu.Unlock()

	am.turnRecorder.FinalizeTurn(agent)
	if am.loopDetector != nil {
		am.loopDetector.ResetThinking()
	}

	pending := am.steering.Flush()
	mainOutput := am.companionBuf.String()
	am.companionBuf.Reset()

	am.companion.RunPostTurn(mainOutput, am.emitFlash)

	if len(pending) > 0 {
		merged := strings.Join(pending, "\n")
		am.mu.Lock()
		am.pendingSteering = merged
		am.mu.Unlock()
	}
}

func (am *AgentManager) logEvent(event agentic.OutputEvent) {
	am.mu.Lock()
	logger := am.logger
	am.mu.Unlock()
	if logger == nil || !logger.Enabled(agentic.Debug) {
		return
	}

	parts := []string{string(event.Type)}
	if event.State != 0 {
		parts = append(parts, "state="+event.State.String())
	}
	if event.Role != "" {
		parts = append(parts, "role="+string(event.Role))
	}
	if event.ToolName != "" {
		parts = append(parts, "tool="+event.ToolName)
	}
	if event.ToolInput != "" {
		parts = append(parts, "input="+event.ToolInput)
	}
	if event.ToolCallID != "" {
		parts = append(parts, "call_id="+event.ToolCallID)
	}
	if event.ToolResult != "" {
		parts = append(parts, "result="+event.ToolResult)
	}
	if event.Text != "" {
		parts = append(parts, "text="+event.Text)
	}
	logger.Log(agentic.Debug, "[agent event] %s", strings.Join(parts, " "))
}
