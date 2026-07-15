// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strconv"
	"strings"
	"sync"

	"github.com/pijalu/goa/internal/agentic"
	"github.com/pijalu/goa/internal/tooltracker"
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
	// tracker owns this agent's tool widgets (one per logical call, with
	// late-id adoption). Replaces the per-state tools map + activeTool fallback
	// that could orphan streaming widgets.
	tracker *tooltracker.Tracker
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
}

// newAgentStreamRegistry returns an empty registry.
func newAgentStreamRegistry() *agentStreamRegistry {
	return &agentStreamRegistry{
		streams: map[string]*agentStreamState{},
	}
}

// begin starts or reuses a stream for agentID. The label is disambiguated only
// when another stream of the same role is currently active; when the previous
// agent of that role has finished, the base role label is reused so the UI does
// not create "coder·2", "coder·3" for sequential delegations to the same role.
func (r *agentStreamRegistry) begin(role, agentID string) *agentStreamState {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.streams[agentID]; ok {
		return s
	}
	label := r.nextLabel(role)
	s := &agentStreamState{label: label}
	r.streams[agentID] = s
	return s
}

// nextLabel returns the smallest available label for role. It prefers the base
// role name and only adds a ·N suffix when necessary for concurrent agents.
func (r *agentStreamRegistry) nextLabel(role string) string {
	used := map[int]struct{}{}
	for _, s := range r.streams {
		if s.label == role {
			used[1] = struct{}{}
			continue
		}
		if !strings.HasPrefix(s.label, role+"·") {
			continue
		}
		suffix := strings.TrimPrefix(s.label, role+"·")
		if n, err := strconv.Atoi(suffix); err == nil && n > 0 {
			used[n] = struct{}{}
		}
	}
	if _, ok := used[1]; !ok {
		return role
	}
	n := 2
	for {
		if _, ok := used[n]; !ok {
			return fmt.Sprintf("%s·%d", role, n)
		}
		n++
	}
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

// end closes any open segment for agentID and removes the stream so the label
// can be reused for a later agent of the same role.
func (r *agentStreamRegistry) end(agentID string) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if s, ok := r.streams[agentID]; ok {
		s.endSegment()
		delete(r.streams, agentID)
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

func (a *App) handleAgentThinking(agentID, text string, isDelta bool) {
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
	if isDelta {
		state.thinking.WriteString(text)
	} else {
		state.thinking.Reset()
		state.thinking.WriteString(text)
	}
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

func (a *App) handleAgentContent(agentID, text string, isDelta bool) {
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
	if isDelta {
		state.content.WriteString(text)
	} else {
		state.content.Reset()
		state.content.WriteString(text)
	}
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

func (a *App) handleAgentToolCall(agentID, name, input, callID string, isDelta bool) {
	if a.subs.agentStreams == nil || a.subs.chat == nil {
		return
	}
	state := a.subs.agentStreams.get(agentID)
	if state == nil {
		return
	}
	state.endSegment()

	a.agentTracker(state).OnCall(&agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		ToolName:   name,
		ToolInput:  input,
		ToolCallID: callID,
		IsDelta:    isDelta,
	})
	a.showToolStatus(state, name)
}

// agentTracker returns the per-agent tool-call tracker, lazily binding it to
// the chat viewport with the agent's label so every widget it creates is
// attributed to this agent.
func (a *App) agentTracker(state *agentStreamState) *tooltracker.Tracker {
	if state.tracker == nil {
		chat := a.subs.chat
		label := state.label
		state.tracker = tooltracker.New(func(name, input string) *tui.ToolExecutionComponent {
			if chat == nil {
				return nil
			}
			return chat.AddAgentToolExecution(label, name, input)
		})
	}
	return state.tracker
}

// showToolStatus updates the status bar to identify the tool being called.
func (a *App) showToolStatus(state *agentStreamState, name string) {
	if a.subs.statusMsg == nil {
		return
	}
	// Include the tool name so the spinner identifies which tool the agent
	// is invoking (e.g. "coder tool calling: bash"), not just the generic
	// "tool calling". Keeps the "tool calling" substring so existing
	// footer/status assertions still match.
	a.subs.statusMsg.Show(state.label + " tool calling: " + name)
}

func (a *App) handleAgentToolResult(agentID, callID, text string, ok bool) {
	if a.subs.agentStreams == nil || a.subs.chat == nil {
		return
	}
	state := a.subs.agentStreams.get(agentID)
	if state == nil {
		return
	}
	// Close any open segment so the next thinking/content chunk starts a
	// fresh block rather than appending to the pre-tool block.
	state.endSegment()

	if state.tracker != nil {
		tc := state.tracker.OnResultOK(&agentic.OutputEvent{
			Type:       agentic.EventToolResult,
			ToolCallID: callID,
			Text:       text,
		}, ok)
		if tc != nil {
			return
		}
	}
	// Fallback: render a plain tool result entry if no matching widget exists.
	a.subs.chat.AddToolResult(text)
}

