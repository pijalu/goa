// SPDX-License-Identifier: GPL-3.0-or-later

package multiagent

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/agentic"
)

type agentOutputState struct {
	agentBuf       string
	thinkingBuf    string
	streamActive   bool
	thinkingActive bool
}

func handleAgentOutputEvent(o *ForegroundOrchestrator, role string, state *agentOutputState, ev agentic.OutputEvent) {
	switch ev.Type {
	case agentic.EventToolCall:
		// Track tool calls during this stage. WorkflowNextTool uses this
		// to validate that actual work was done before allowing advancement.
		if ev.ToolName != "workflows_next" {
			o.stageToolCount.Add(1)
		}
		// Surface sub-agent tool activity to the TUI: without this, companion
		// sections show only thinking and the user never sees what the
		// sub-agent actually does (team UI bug RC-2).
		o.emitKind(role, "tool_call", ev.ToolName, "tool_call")
	case agentic.EventToolResult:
		// Companion/delegate tool results: forward a short preview so the
		// role's section shows completion of the tool call. Full results stay
		// in the sub-agent's history; the TUI line is a status marker only.
		o.emitKind(role, "tool_result", toolResultPreview(ev.Text), "tool_result")
	case agentic.EventContent:
		handleAgentContentEvent(o, role, state, ev)
	case agentic.EventEnd:
		handleAgentEndEvent(o, role, state)
	}
}

// toolResultPreview truncates a tool result to a single-line preview for the
// TUI section tool marker.
func toolResultPreview(text string) string {
	text = strings.TrimSpace(text)
	if i := strings.IndexByte(text, '\n'); i >= 0 {
		text = text[:i]
	}
	const maxPreview = 80
	if len(text) > maxPreview {
		text = text[:maxPreview] + "…"
	}
	return text
}

func handleAgentContentEvent(o *ForegroundOrchestrator, role string, state *agentOutputState, ev agentic.OutputEvent) {
	if ev.Text == "" || ev.Role != agentic.Assistant {
		return
	}
	if ev.State == agentic.StateThinking {
		if !state.thinkingActive {
			state.thinkingActive = true
			state.thinkingBuf = ""
			o.emitKind(role, "stream_start", "", "thinking_start")
		}
		state.thinkingBuf += ev.Text
		o.emitKind(role, "stream_chunk", ev.Text, "thinking_chunk")
		return
	}
	if state.thinkingActive {
		state.finishThinking(o, role)
	}
	if !state.streamActive {
		state.streamActive = true
		o.emitKind(role, "stream_start", "", "content")
	}
	o.emitKind(role, "stream_chunk", ev.Text, "content")
	state.agentBuf += ev.Text
}

func handleAgentEndEvent(o *ForegroundOrchestrator, role string, state *agentOutputState) {
	if state.thinkingActive {
		state.finishThinking(o, role)
	}
	full := state.agentBuf
	state.agentBuf = ""
	state.streamActive = false

	o.RecordOutput(role, full)
	o.emitKind(role, "stream_end", full, "content")
}

func (s *agentOutputState) finishThinking(o *ForegroundOrchestrator, role string) {
	s.thinkingActive = false
	o.emitKind(role, "thinking_end", s.thinkingBuf, "thinking_end")
	s.thinkingBuf = ""
}

func (o *ForegroundOrchestrator) emitGateApproval(stageID, stageName, prompt string) {
	// The TUI layer will detect the special gate message format and show a selector
	o.emit("gate", "user", fmt.Sprintf("GATE_APPROVAL:%s|%s|%s", stageID, stageName, prompt))
}
