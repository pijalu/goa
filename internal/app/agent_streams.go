// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"
	"sync"

	"github.com/pijalu/goa/tui"
)

// agentStreamState tracks one agent's in-place streaming blocks in the chat
// viewport. Each agent gets its own state so multiple agents can stream
// concurrently without overwriting each other.
type agentStreamState struct {
	label       string
	thinking    strings.Builder
	content     strings.Builder
	kind        tui.ConsoleItemType
	thinkView   tui.Component
	contentView tui.Component
	tools       map[string]*tui.ToolExecutionComponent
}

// endSegment closes the current thinking/content segment so the next chunk
// starts a fresh block. It resets the in-memory buffers and clears the view
// references so the next chunk creates a new component.
func (s *agentStreamState) endSegment() {
	s.kind = 0
	s.thinking.Reset()
	s.content.Reset()
	s.thinkView = nil
	s.contentView = nil
}

// agentStreamRegistry owns per-agent streaming state. It is only non-nil during
// orchestration; the main-agent path ignores it.
type agentStreamRegistry struct {
	mu      sync.Mutex
	streams map[string]*agentStreamState // key = agentID
	labels  map[string]int                // role → seen count for disambiguation
}

// newAgentStreamRegistry returns an empty registry.
func newAgentStreamRegistry() *agentStreamRegistry {
	return &agentStreamRegistry{
		streams: map[string]*agentStreamState{},
		labels:  map[string]int{},
	}
}

// begin starts or reuses a stream for agentID. The label is disambiguated so
// recurring roles get "coder", "coder·2", etc.
func (r *agentStreamRegistry) begin(role, agentID string) *agentStreamState {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.streams[agentID]; ok {
		return s
	}
	r.labels[role]++
	label := role
	if r.labels[role] > 1 {
		label = fmt.Sprintf("%s·%d", role, r.labels[role])
	}
	s := &agentStreamState{label: label, tools: map[string]*tui.ToolExecutionComponent{}}
	r.streams[agentID] = s
	return s
}

// get returns the stream for agentID, or nil if unknown.
func (r *agentStreamRegistry) get(agentID string) *agentStreamState {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.streams[agentID]
}

// end closes any open segment for agentID.
func (r *agentStreamRegistry) end(agentID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.streams[agentID]; ok {
		s.endSegment()
	}
}

// reconcileAgentContent snaps an agent's displayed content to the
// authoritative full text. It is called on EvAgentFinished so that any
// content deltas dropped by the live fanout (a lossy best-effort path) are
// repaired from the durable source of truth — the handle's accumulated
// message, carried on the finished event. Thinking is intentionally not
// reconciled (it is transient and may be lossy by design).
func (a *App) reconcileAgentContent(agentID, text string) {
	if a.subs.agentStreams == nil || a.subs.chat == nil || text == "" {
		return
	}
	state := a.subs.agentStreams.get(agentID)
	if state == nil {
		return
	}
	state.content.Reset()
	state.content.WriteString(text)
	if state.contentView != nil {
		a.subs.chat.UpdateAgentContent(state.label, text)
	}
}

func (a *App) beginAgentStream(role, agentID string) {
	if a.subs.agentStreams == nil {
		return
	}
	a.subs.agentStreams.begin(role, agentID)
}

func (a *App) endAgentStream(agentID string) {
	if a.subs.agentStreams == nil {
		return
	}
	a.subs.agentStreams.end(agentID)
}

func (a *App) handleAgentThinking(agentID, text string) {
	if a.subs.agentStreams == nil || a.subs.chat == nil {
		return
	}
	if a.subs.cfg != nil && !a.subs.cfg.TUI.Transparency.ShowThinking {
		return
	}
	state := a.subs.agentStreams.get(agentID)
	if state == nil {
		return
	}
	if state.kind != tui.ConsoleThinkingBlock && state.kind != 0 {
		state.endSegment()
	}
	state.thinking.WriteString(text)
	if state.thinkView == nil {
		expanded := a.subs.cfg == nil || !a.subs.cfg.TUI.Transparency.ThinkingCollapsed
		state.thinkView = a.subs.chat.AddAgentThinkingBlock(state.label, state.thinking.String(), expanded)
		state.kind = tui.ConsoleThinkingBlock
	} else {
		a.subs.chat.UpdateAgentThinking(state.label, state.thinking.String())
	}
	if a.subs.statusMsg != nil {
		a.subs.statusMsg.Show(state.label + " thinking...")
	}
}

func (a *App) handleAgentContent(agentID, text string) {
	if a.subs.agentStreams == nil || a.subs.chat == nil {
		return
	}
	state := a.subs.agentStreams.get(agentID)
	if state == nil {
		return
	}
	if state.kind != tui.ConsoleAgentMessage && state.kind != 0 {
		state.endSegment()
	}
	state.content.WriteString(text)
	if state.contentView == nil {
		state.contentView = a.subs.chat.AddAgentContent(state.label, state.content.String())
		state.kind = tui.ConsoleAgentMessage
	} else {
		a.subs.chat.UpdateAgentContent(state.label, state.content.String())
	}
	if a.subs.statusMsg != nil {
		a.subs.statusMsg.Show(state.label + " answering...")
	}
}

func (a *App) handleAgentToolCall(agentID, name, input, callID string) {
	if a.subs.agentStreams == nil || a.subs.chat == nil {
		return
	}
	state := a.subs.agentStreams.get(agentID)
	if state == nil {
		return
	}
	state.endSegment()
	tc := a.subs.chat.AddAgentToolExecution(state.label, name, input)
	if callID != "" {
		state.tools[callID] = tc
	}
	if a.subs.statusMsg != nil {
		// Include the tool name so the spinner identifies which tool the agent
		// is invoking (e.g. "coder tool calling: bash"), not just the generic
		// "tool calling". Keeps the "tool calling" substring so existing
		// footer/status assertions still match.
		a.subs.statusMsg.Show(state.label + " tool calling: " + name)
	}
}

func (a *App) handleAgentToolResult(agentID, callID, text string, ok bool) {
	if a.subs.agentStreams == nil || a.subs.chat == nil {
		return
	}
	state := a.subs.agentStreams.get(agentID)
	if state == nil {
		return
	}
	if tc, okTool := state.tools[callID]; okTool {
		status := tui.ToolSuccess
		if !ok {
			status = tui.ToolError
		}
		tc.SetOutput(text)
		tc.SetStatus(status)
		tc.SetPartial(false)
		delete(state.tools, callID)
		// Close any open segment so the next thinking/content chunk starts a
		// fresh block rather than appending to the pre-tool block.
		state.endSegment()
		return
	}
	// Fallback: render a plain tool result entry if no matching widget exists.
	a.subs.chat.AddToolResult(text)
}

