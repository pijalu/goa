// SPDX-License-Identifier: GPL-3.0-or-later

package agentic

import (
	"strings"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func (a *Agent) handleTextDelta(event provider.AssistantMessageEvent) {
	a.resetThinkingStall()
	a.cfg.Logger.Log(Trace, "[delta] content: %s", event.Delta)
	a.mu.Lock()
	a.turnSawContent = true
	a.mu.Unlock()
	a.contentBuf.WriteString(event.Delta)
	a.checkStreamLoop(a.contentBuf.String())

	// Display path: stream live until a tool-call markup signal appears; from
	// then on, buffer and emit only markup-free text so multi-delta tool-call
	// markup (DSML on tool_choice:"none" collapse rounds, or healed XML) is
	// never rendered raw. The raw contentBuf is untouched for healing/finalize.
	if !a.contentMarkupSeen && hasToolSignal(event.Delta) {
		a.contentMarkupSeen = true
	}
	if !a.contentMarkupSeen {
		a.emitEvent(OutputEvent{Type: EventContent, State: StateContent, Role: Assistant, Text: event.Delta, IsDelta: true})
		return
	}
	a.contentDisplayBuf.WriteString(event.Delta)
	clean := stripToolMarkup(a.contentDisplayBuf.String(), true)
	if clean != "" && !containsToolXMLTag(clean) {
		a.emitEvent(OutputEvent{Type: EventContent, State: StateContent, Role: Assistant, Text: clean, IsDelta: true})
		a.contentDisplayBuf.Reset()
	}
}

func (a *Agent) handleThinkingDelta(event provider.AssistantMessageEvent) {
	a.cfg.Logger.Log(Trace, "[delta] thinking: %s", event.Delta)
	a.mu.Lock()
	a.turnSawThinking = true
	a.mu.Unlock()
	a.thinkingBuf.WriteString(event.Delta)
	a.checkStreamLoop(a.thinkingBuf.String())

	// Track extended thinking without progress. This watchdog is independent
	// from the stream loop detector and toggled separately
	// (/config:temp:thinking_stall_detection:off or
	// execution.disable_thinking_stall_detection); checked per delta so a
	// mid-stream toggle takes effect immediately.
	//
	// A thinking delta is itself progress: the stall clock measures the gap
	// since the LAST received thinking delta, so a slow model that streams
	// reasoning tokens continuously for longer than the stop threshold is
	// never stopped (session export 2026-08-15: the pre-fix code measured
	// from the first delta of the phase and killed an actively-streaming
	// locallm exactly thinking_stall_stop_seconds later). Because a true
	// no-delta hang delivers no further deltas that could re-run this check,
	// re-armed timers detect the gap even when the stream goes silent.
	if stallDisabled := a.cfg.ThinkingStallDisabled != nil && a.cfg.ThinkingStallDisabled(); !stallDisabled {
		warnAfter := a.cfg.ThinkingStallWarn
		if warnAfter <= 0 {
			warnAfter = defaultThinkingStallWarn
		}
		stopAfter := a.cfg.ThinkingStallStop
		if stopAfter <= 0 {
			stopAfter = defaultThinkingStallStop
		}
		now := time.Now()
		// First delta after a silence gap longer than the stop threshold:
		// the timer should have fired but cannot be relied on when the gap
		// crosses a round/reset boundary, so evaluate the gap inline too.
		if !a.thinkingStallStart.IsZero() && now.Sub(a.thinkingStallStart) > stopAfter {
			a.markThinkingStalled(now.Sub(a.thinkingStallStart))
			return
		}
		a.armThinkingStallTimers(now, warnAfter, stopAfter)
	}

	// Strip tool-call XML from the visible thinking stream. Local
	// models sometimes emit <tool_call> or <function=> markup inside
	// reasoning_content; without this, raw XML is rendered in the thinking
	// block. The raw thinking buffer is still accumulated for auto-heal.
	a.thinkingDisplayBuf.WriteString(event.Delta)
	clean := stripToolMarkup(a.thinkingDisplayBuf.String(), true)
	if clean != "" && !containsToolXMLTag(clean) {
		a.emitEvent(OutputEvent{Type: EventContent, State: StateThinking, Role: Assistant, Text: clean, IsDelta: true})
		a.thinkingDisplayBuf.Reset()
	}
}

// handleToolCallPartial processes an incremental tool call event during
// streaming (EventToolCallStart, or EventToolCallDelta when the provider
// ships a full Partial snapshot such as OpenAI). It accumulates partial
// arguments and emits EventToolCall updates to observers so the TUI can
// display live progress as the model constructs the tool call.
//
// contentIndex correlates the call across later nil-Partial deltas (Anthropic
// ships input_json_delta with only Delta + ContentIndex); it is recorded so
// handleToolCallDeltaByIndex can append to the right call.
func (a *Agent) handleToolCallPartial(tc provider.ContentBlock, contentIndex int) {
	id := tc.ToolCallID
	if id == "" {
		id = tc.ToolName // fallback: some providers omit the ID on start
	}

	a.mu.Lock()
	ptc, exists := a.streamingToolCalls[id]
	if !exists {
		ptc = &partialToolCall{
			toolName:     tc.ToolName,
			toolCallID:   tc.ToolCallID,
			contentIndex: contentIndex,
		}
		if a.streamingToolCalls == nil {
			a.streamingToolCalls = make(map[string]*partialToolCall)
		}
		a.streamingToolCalls[id] = ptc
		a.indexStreamingToolCall(contentIndex, ptc)
	}
	if tc.ToolName != "" {
		ptc.toolName = tc.ToolName
	}
	if tc.ToolCallID != "" {
		ptc.toolCallID = tc.ToolCallID
	}
	if tc.ToolArguments != "" {
		ptc.argsBuf.WriteString(tc.ToolArguments)
	}
	accumulated := ptc.argsBuf.String()
	emitID := ptc.toolCallID
	emitName := ptc.toolName
	a.mu.Unlock()

	// Do not emit until the tool name is known: OpenAI-style streams ship the
	// call id/index in the first chunk and the name only in a later one, and a
	// nameless delta made the TUI create a blank-header tool widget that never
	// updated (Empty tool TUI). Args accumulate here and are emitted
	// cumulative, so the first named delta carries the full prefix.
	if emitName == "" {
		return
	}

	// Emit partial EventToolCall to observers (TUI will show ◉ pending icon).
	a.emitEvent(OutputEvent{
		Type:       EventToolCall,
		State:      StateToolCall,
		Role:       Assistant,
		ToolName:   emitName,
		ToolInput:  accumulated,
		ToolCallID: emitID,
		IsDelta:    true,
	})
}

// handleToolCallDeltaByIndex appends an incremental JSON fragment to the
// streaming tool call identified by contentIndex and re-emits a partial
// EventToolCall. This is the Anthropic path: input_json_delta events carry
// only Delta + ContentIndex (no Partial snapshot), so without this the args
// would never stream to the TUI until the whole call completed.
func (a *Agent) handleToolCallDeltaByIndex(contentIndex int, delta string) {
	a.mu.Lock()
	ptc := a.streamingToolCallsByIndex[contentIndex]
	if ptc == nil {
		a.mu.Unlock()
		return
	}
	ptc.argsBuf.WriteString(delta)
	accumulated := ptc.argsBuf.String()
	emitID := ptc.toolCallID
	emitName := ptc.toolName
	a.mu.Unlock()

	// Same nameless guard as handleToolCallPartial (Empty tool TUI).
	if emitName == "" {
		return
	}

	a.emitEvent(OutputEvent{
		Type:       EventToolCall,
		State:      StateToolCall,
		Role:       Assistant,
		ToolName:   emitName,
		ToolInput:  accumulated,
		ToolCallID: emitID,
		IsDelta:    true,
	})
}

// indexStreamingToolCall records a partial call under its content-block index
// so nil-Partial deltas (Anthropic) can be correlated. Caller must hold a.mu.
func (a *Agent) indexStreamingToolCall(contentIndex int, ptc *partialToolCall) {
	if a.streamingToolCallsByIndex == nil {
		a.streamingToolCallsByIndex = make(map[int]*partialToolCall)
	}
	a.streamingToolCallsByIndex[contentIndex] = ptc
}

// containsToolXMLTag reports whether text still contains any raw tool-call XML
// tag (open or close). It is used while streaming thinking text so that
// multi-line tool-call markup that spans multiple deltas is suppressed until
// the whole block is closed and stripped.
func containsToolXMLTag(text string) bool {
	for _, tag := range []string{
		"<tool_call>", "</tool_call>",
		"<function=", "</function>",
		"<parameter=", "</parameter>",
		"<｜｜DSML｜｜", // any DSML delimiter (open or close) suppresses display
	} {
		if strings.Contains(text, tag) {
			return true
		}
	}
	return false
}

// armThinkingStallTimers records a thinking delta as forward progress and
// (re)arms the warn/stop timers. Every received delta pushes the deadlines
// out, so the timers only fire after warnAfter/stopAfter of continuous
// thinking silence — never while deltas are still arriving.
