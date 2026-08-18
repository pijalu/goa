// SPDX-License-Identifier: GPL-3.0-or-later
package core

import (
	"strings"

	"github.com/pijalu/goa/internal"
	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/event"
)

func (am *AgentManager) queueMajorModePrompt(major internal.MajorMode) {
	am.mu.Lock()
	defer am.mu.Unlock()
	if am.activeAgent == nil {
		return
	}
	am.pendingMajor = &major
}
func (am *AgentManager) applyPendingMajorMode() {
	am.mu.Lock()
	pending := am.pendingMajor
	am.pendingMajor = nil
	am.mu.Unlock()
	if pending != nil {
		am.injectModePrompt(*pending)
	}
}
func (am *AgentManager) emitMinorMode(mode string) {
	if am.eventsOut == nil {
		return
	}
	select {
	case am.eventsOut.Footer <- event.FooterEvent{MinorMode: &event.MinorMode{Mode: mode}}:
	default:
		{
		}
	}
}
func (am *AgentManager) emitThinkingLevel(level string) {
	if am.eventsOut == nil {
		return
	}
	select {
	case am.eventsOut.Footer <- event.FooterEvent{ThinkingLevel: &event.ThinkingLevel{Level: level}}:
	default:
		{
		}
	}
}
func (am *AgentManager) handleThinkingLoopWarning(lvl LoopWarningLevel) {
	if lvl <= LoopOK {
		return
	}
	switch lvl {
	case LoopWarning:
		am.logEventF("loop detector: warning (thinking repeat)")
		am.emitFlash("[goa-system: warning] Reasoning is repeating — the model may be stuck in a thinking loop.")
	case LoopCritical, LoopInterrupt:
		am.logEventF("loop detector: interrupt — thinking loop detected, cancelling turn")
		am.emitFlash("[goa-system: interrupt] Thinking loop detected — cancelling turn.")
		am.setLoopStopReason("[goa-system] Agent stopped: the model kept repeating the same line of reasoning (thinking loop). Rephrase the request, provide more context, or disable thinking-loop detection (/config → Loop detection).")
		am.loopDetector.ResetThinking()
		am.Interrupt()
	}
}
func (am *AgentManager) handleLoopWarning(lvl LoopWarningLevel) {
	if lvl <= LoopOK {
		return
	}
	switch lvl {
	case LoopWarning:
		am.logEventF("loop detector: warning (tool repeat)")
		am.emitFlash("[goa-system: warning] Tool call repeated — consider completing the task.")
	case LoopCritical:
		am.logEventF("loop detector: critical — cancelling turn")
		am.emitFlash("[goa-system: critical] Agent looping — cancelling turn.")
		am.setLoopStopReason("[goa-system] Agent stopped: the same tool call was repeated too many times without progress. Change approach or provide the final answer.")
		am.Interrupt()
	case LoopInterrupt:
		am.logEventF("loop detector: interrupt — cancelling turn")
		am.emitFlash("[goa-system: interrupt] Tool call loop detected — cancelling turn.")
		am.setLoopStopReason("[goa-system] Agent stopped: a tool-call loop was detected (the same call repeated too many times). Change approach or provide the final answer.")
		am.Interrupt()
	}
}
func (am *AgentManager) setLoopStopReason(reason string) {
	am.mu.Lock()
	defer am.mu.Unlock()
	am.loopStopReason = reason
}
func (am *AgentManager) logEventF(format string, args ...interface{}) {
	am.mu.Lock()
	logger := am.logger
	am.mu.Unlock()
	if logger != nil && logger.Enabled(agentic.Warn) {
		logger.Log(agentic.Warn, format, args...)
	}
}
func (am *AgentManager) LoopDetector() *LoopDetector { return am.loopDetector }
func isToolResultError(result string) bool {
	return result == "" || strings.HasPrefix(result, "Error:")
}
