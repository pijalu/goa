// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"context"
	"strings"
	"time"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
)

func (h *HeadlessApp) runAgentEventReader(ctx context.Context, dc *doneCloser) {
	defer dc.close()
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-h.subs.agentMgr.Events():
			if !ok {
				return
			}
			h.handleAgentEvent(&ev)
		}
	}
}

func (h *HeadlessApp) runOrchestratorEventReader(ctx context.Context, dc *doneCloser) {
	if h.subs.foregroundOrch == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-dc.done:
			return
		case m, ok := <-h.subs.foregroundOrch.Events():
			if !ok {
				return
			}
			h.handleOrchestratorMessage(m)
		}
	}
}

func (h *HeadlessApp) waitForIdle(ctx context.Context, dc *doneCloser) {
	if h.subs.agentMgr == nil {
		dc.close()
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(50 * time.Millisecond):
			if !h.subs.agentMgr.IsRunning() {
				// Give in-flight events a moment to drain before signaling done.
				time.Sleep(100 * time.Millisecond)
				dc.close()
				return
			}
		}
	}
}

// waitForGoal waits until there is no active goal and the agent is idle.
// The GoalDriver runs continuation turns in the background; this loop avoids
// exiting while a turn is in flight or while the goal can still continue.
func (h *HeadlessApp) waitForGoal(ctx context.Context, dc *doneCloser) {
	if h.subs.agentMgr == nil || h.subs.goalManager == nil {
		dc.close()
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(100 * time.Millisecond):
			if !h.goalActive() && !h.subs.agentMgr.IsRunning() {
				// Give in-flight events a moment to drain before signaling done.
				time.Sleep(100 * time.Millisecond)
				dc.close()
				return
			}
		}
	}
}

func (h *HeadlessApp) runConfirmationReader(ctx context.Context, dc *doneCloser) {
	if h.subs.execCtrl == nil {
		return
	}
	consumer := func(c context.Context, req internal.ConfirmRequest) error {
		approved, err := h.confirm.Confirm(req.ToolName, req.ToolInput)
		if err != nil {
			select {
			case req.ResponseChan <- internal.ConfirmNo:
			case <-c.Done():
			}
			return err
		}
		resp := internal.ConfirmNo
		if approved {
			resp = internal.ConfirmYes
		}
		select {
		case req.ResponseChan <- resp:
		case <-c.Done():
			return c.Err()
		}
		return nil
	}
	h.subs.execCtrl.SetConfirmConsumer(func(c context.Context, req internal.ConfirmRequest) error {
		select {
		case <-dc.done:
			return context.Canceled
		case <-ctx.Done():
			return ctx.Err()
		default:
			return consumer(c, req)
		}
	})
	// Keep the consumer registered until the session is done or the context
	// is cancelled. This avoids the legacy queue-based race where a tool call
	// would default to ConfirmYes if no listener was immediately ready.
	select {
	case <-dc.done:
	case <-ctx.Done():
	}
}

func (h *HeadlessApp) handleAgentEvent(ev *agentic.OutputEvent) {
	switch ev.Type {
	case agentic.EventContent:
		h.handleContentEvent(ev)
	case agentic.EventToolCall:
		h.handleToolCallEvent(ev)
	case agentic.EventToolResult:
		h.handleToolResultEvent(ev)
	case agentic.EventEnd:
		h.handleEndEvent(ev)
	case agentic.EventTokenStats, agentic.EventContextStats:
		h.handleStatsEvent(ev)
	case agentic.EventCompact:
		h.recordCompact(ev)
	case agentic.EventProgress:
		// Progress/status messages are not rendered in headless mode.
	}
}

func (h *HeadlessApp) handleContentEvent(ev *agentic.OutputEvent) {
	if ev.Role == agentic.User || ev.Role == agentic.System {
		// F6: agent-injected messages (companion reviews delivered via the
		// agent bus) arrive as User-role content events and were previously
		// swallowed, so headless --plain output never showed the review. The
		// initial prompt is rendered separately at startup; only bus-delivered
		// messages (formatted "[Message from <agent>]: ...") are rendered here.
		h.renderAgentBusMessage(ev)
		return
	}
	switch ev.State {
	case agentic.StateThinking:
		if !h.stream.is(headlessStreamThinking) {
			if h.stream.active() {
				h.endStream()
			}
			h.renderer.ThinkingStart()
			h.stream.begin(headlessStreamThinking)
		}
		h.renderer.ThinkingChunk(ev.Text)
		h.stream.text.WriteString(ev.Text)
	default:
		if h.stream.is(headlessStreamThinking) {
			h.renderer.ThinkingEnd()
			h.stream.end()
		}
		if !h.stream.is(headlessStreamAssistant) {
			if h.stream.active() {
				h.endStream()
			}
			h.stream.begin(headlessStreamAssistant)
		}
		h.renderer.AssistantChunk(ev.Text)
		h.stream.text.WriteString(ev.Text)
	}
}

// isAgentBusMessage reports whether a User-role content event is an
// agent-injected bus message ("[Message from <agent>]: ..."), e.g. a companion
// review delivered via sendToMain (multiagent/agent_driven_tools.go).
func isAgentBusMessage(ev *agentic.OutputEvent) bool {
	return ev.Role == agentic.User && strings.HasPrefix(ev.Text, "[Message from ")
}

// renderAgentBusMessage renders a bus-delivered agent message (companion
// review) in headless --plain output, ending any in-flight stream first.
// Non-bus User/System events (e.g. the initial prompt, rendered at startup)
// are a no-op.
func (h *HeadlessApp) renderAgentBusMessage(ev *agentic.OutputEvent) {
	if !isAgentBusMessage(ev) {
		return
	}
	if h.stream.active() {
		h.endStream()
	}
	h.renderer.UserPrompt(ev.Text)
}

func (h *HeadlessApp) handleToolCallEvent(ev *agentic.OutputEvent) {
	if h.stream.active() {
		h.endStream()
	}
	if ev.ToolCallID != "" {
		h.toolCallNamesMu.Lock()
		h.toolCallNames[ev.ToolCallID] = ev.ToolName
		h.toolCallNamesMu.Unlock()
	}
	h.statsMu.Lock()
	h.toolCallsTotal++
	h.statsMu.Unlock()
	h.renderer.ToolCall(ev.ToolName, ev.ToolCallID, ev.ToolInput)
}

func (h *HeadlessApp) handleToolResultEvent(ev *agentic.OutputEvent) {
	if h.stream.active() {
		h.endStream()
	}
	toolName := ev.ToolName
	if toolName == "" && ev.ToolCallID != "" {
		h.toolCallNamesMu.Lock()
		toolName = h.toolCallNames[ev.ToolCallID]
		h.toolCallNamesMu.Unlock()
	}
	h.renderer.ToolResult(toolName, ev.ToolCallID, ev.Text)
}

func (h *HeadlessApp) handleEndEvent(ev *agentic.OutputEvent) {
	if h.stream.active() {
		h.endStream()
	}

	h.statsMu.Lock()
	h.turnCount++
	stats := h.buildStatsLocked()
	turn := h.turnCount
	exceeded := h.opts.MaxTurns > 0 && h.turnCount >= h.opts.MaxTurns
	h.statsMu.Unlock()

	h.renderer.Stats(stats, turn)

	if ev != nil && ev.Text != "" {
		h.renderer.Error(friendlyConnectionHint(ev.Text))
	} else if ev != nil && ev.Metadata["cancelled"] == "true" {
		h.renderer.Error("Generation stopped by user.")
	}

	if exceeded && h.subs.agentMgr != nil {
		h.subs.agentMgr.Interrupt()
	}
}

func (h *HeadlessApp) handleStatsEvent(ev *agentic.OutputEvent) {
	h.statsMu.Lock()
	defer h.statsMu.Unlock()
	if ev.Timings != nil {
		h.lastTurnPromptN = ev.Timings.PromptN
		h.lastTurnPredictedN = ev.Timings.PredictedN
		h.tokenPromptTotal += ev.Timings.PromptN
		h.tokenPredictedTotal += ev.Timings.PredictedN
		h.lastTurnCacheRead = ev.Timings.CacheReadTokens
		h.lastTurnCacheWrite = ev.Timings.CacheWriteTokens
		h.tokenCacheReadTotal += ev.Timings.CacheReadTokens
		h.tokenCacheWriteTotal += ev.Timings.CacheWriteTokens
		h.lastTurnSpeed = ev.Timings.PredictedPerSecond
		if h.lastTurnSpeed == 0 && ev.Timings.PredictedMs > 0 {
			h.lastTurnSpeed = float64(ev.Timings.PredictedN) / (ev.Timings.PredictedMs / 1000.0)
		}
	}
	if ev.ContextStats != nil {
		h.tokenSessionMax = ev.ContextStats.MaxTokens
		h.tokenSessionEstimate = ev.ContextStats.EstimatedTokens
		h.tokenSessionProjected = ev.ContextStats.ProjectedTokens
	}
}

// recordCompact counts one completed compression pass and appends its
// per-round record, mirroring App.recordCompact so headless and TUI session
// stats classify and document compressions identically.
func (h *HeadlessApp) recordCompact(ev *agentic.OutputEvent) {
	strategy := compactionStrategy(ev)
	h.statsMu.Lock()
	defer h.statsMu.Unlock()
	if isMicroCompaction(strategy) {
		h.microCompacts++
	} else {
		h.compacts++
	}
	h.compactions = append(h.compactions, compactionRoundFromEvent(ev, strategy))
}

func (h *HeadlessApp) endStream() {
	if h.stream.is(headlessStreamThinking) {
		h.renderer.ThinkingEnd()
	} else if h.stream.is(headlessStreamAssistant) {
		h.renderer.AssistantStreamEnd()
	}
	h.stream.end()
}

func (h *HeadlessApp) buildStats() sessionStats {
	h.statsMu.Lock()
	defer h.statsMu.Unlock()
	return h.buildStatsLocked()
}

func (h *HeadlessApp) buildStatsLocked() sessionStats {
	st := sessionStats{
		PromptN:          h.tokenPromptTotal,
		PredictedN:       h.tokenPredictedTotal,
		SpeedTokPerSec:   h.lastTurnSpeed,
		ContextEstimate:  h.tokenSessionEstimate,
		ContextProjected: h.tokenSessionProjected,
		ContextMax:       h.tokenSessionMax,
		ToolCalls:        h.toolCallsTotal,
		CacheReadTotal:   h.tokenCacheReadTotal,
		CacheWriteTotal:  h.tokenCacheWriteTotal,
		LastCacheHit:     cacheHitTrendFromTotals(h.lastTurnCacheRead, h.lastTurnCacheWrite, h.lastTurnPromptN),
		MicroCompacts:    h.microCompacts,
		Compacts:         h.compacts,
		Compactions:      append([]CompactionRound(nil), h.compactions...),
	}
	if h.subs != nil {
		applyPricing(&st, h.subs.cfg, h.subs.cfg.ActiveModel)
	}
	return st
}
