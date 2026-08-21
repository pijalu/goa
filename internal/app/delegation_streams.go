// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"strings"

	"github.com/pijalu/goa/internal/event"
	"github.com/pijalu/goa/multiagent"
	"github.com/pijalu/goa/tui"
	"github.com/pijalu/goa/tui/agentctx"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
)

// delegation_streams.go is the T4 routing core: it applies the neutral
// delegation-source AgentViewEvents (translated by translateDelegationMsg) to
// the per-agent view registry, so every delegate_to / request_review stream
// lands in ITS OWN AgentTranscript — keyed by DelegationID — instead of the
// shared main ChatViewport interleave (the retired AddCompanionCycle path).
//
// Ownership (R1): every method here mutates registry/transcript state and must
// run on the TUI command loop (the forwarder applies via a.apply). Inactive
// views are pure data: appending to an unmounted transcript performs no
// terminal writes; the pull-based tab strip picks new tabs up on the next
// frame.

// delegationStreamState tracks one delegation's in-place streaming blocks
// inside its own transcript (thinking + content), mirroring the per-agent
// agentStreamState shape but scoped to the multiagent delegation feed (which
// carries no tool-call events).
type delegationStreamState struct {
	thinking    strings.Builder
	content     strings.Builder
	kind        tui.ConsoleItemType
	thinkView   tui.Component
	contentView tui.Component
}

// endSegment closes the current thinking/content segment so the next chunk
// starts a fresh block (mirrors agentStreamState.endSegment).
func (s *delegationStreamState) endSegment() {
	s.kind = 0
	s.thinking.Reset()
	s.content.Reset()
	s.thinkView = nil
	s.contentView = nil
}

// delegationStreamRegistry owns the per-delegation streaming states, keyed by
// delegation id. Command-loop only: no locking.
type delegationStreamRegistry struct {
	streams map[string]*delegationStreamState
}

// get returns the state for id, creating it on first use.
func (r *delegationStreamRegistry) get(id string) *delegationStreamState {
	if r.streams == nil {
		r.streams = map[string]*delegationStreamState{}
	}
	s, ok := r.streams[id]
	if !ok {
		s = &delegationStreamState{}
		r.streams[id] = s
	}
	return s
}

// end closes any open segment for id and drops the state (the delegation is
// terminal; a later same-id event would start fresh — ids are unique per
// delegation, so this is defensive).
func (r *delegationStreamRegistry) end(id string) {
	if s, ok := r.streams[id]; ok {
		s.endSegment()
		delete(r.streams, id)
	}
}

// delegationStreams returns the app's per-delegation stream registry, lazily
// created so headless/tests without createTUIComponents work unchanged.
func (a *App) delegationStreams() *delegationStreamRegistry {
	if a.subs.delegationStreams == nil {
		a.subs.delegationStreams = &delegationStreamRegistry{}
	}
	return a.subs.delegationStreams
}

// handleDelegationViewEvent applies one neutral delegation-source event to the
// per-agent registry. Must run on the command loop (inside a.apply).
func (a *App) handleDelegationViewEvent(ne orchpanel.AgentViewEvent) {
	id := ne.DelegationID
	if id == "" {
		return
	}
	switch ne.Kind {
	case orchpanel.EvAgentStarted:
		// Tab spawns the moment the delegation is created (bug-2 fix: the
		// delegation is visible even before its first streamed chunk).
		a.setDelegationStatus(id, multiagent.DelegationRunning)
		a.ensureDelegationView(id, ne.Role)
	case orchpanel.EvAgentThinking:
		a.handleDelegationThinking(id, ne.Text, ne.IsDelta)
	case orchpanel.EvAgentMessage:
		a.handleDelegationContent(id, ne.Text, ne.IsDelta)
	case orchpanel.EvAgentFinished:
		a.handleDelegationFinished(id, ne.Role, ne.Status, ne.Text)
	}
}

// setDelegationStatus records a delegation's lifecycle status (T5). The map
// is lazily created; entries persist after terminal states so the active-tab
// prompt/footer can keep reporting "completed"/"failed".
func (a *App) setDelegationStatus(id, status string) {
	if a.subs.delegationStatuses == nil {
		a.subs.delegationStatuses = map[string]string{}
	}
	a.subs.delegationStatuses[id] = status
}

// delegationStatus returns the recorded lifecycle status for id ("", unknown).
func (a *App) delegationStatus(id string) string {
	return a.subs.delegationStatuses[id]
}

// hasRunningDelegations reports whether any tracked delegation is still
// running — the condition for the "steer all" prompt label on the main tab.
func (a *App) hasRunningDelegations() bool {
	for _, st := range a.subs.delegationStatuses {
		if st == multiagent.DelegationRunning {
			return true
		}
	}
	return false
}

// ensureDelegationView returns the registry view for delegationID, creating
// (and thereby surfacing a tab for) it on first sight. Creation marks the
// background-activity badge state and requests a render so the tab strip
// appears immediately.
func (a *App) ensureDelegationView(delegationID, role string) *agentctx.AgentView {
	reg := a.subs.agentRegistry
	if reg == nil {
		return nil
	}
	if v, ok := reg.Get(delegationID); ok {
		return v
	}
	tr := agentctx.NewAgentTranscript(delegationID)
	// Apply the same chat configuration the main transcript got (tool view
	// expansion/preview), so delegation transcripts render identically.
	a.configureChat(tr.View())
	v := &agentctx.AgentView{Transcript: tr, Compositor: tr.Compositor()}
	reg.Add(delegationID, v)
	// The new tab is background (main stays active): record activity so the
	// badge state reflects the unseen work, and repaint so the strip shows.
	reg.MarkActivity(delegationID)
	// A new live delegation flips the active-tab chrome: the main tab's
	// prompt becomes "steer all" while it runs.
	a.refreshAgentCtxChrome()
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.RequestRender()
	}
	return v
}

// delegationView returns the transcript viewport for id, or nil when the
// registry/view is unavailable (headless, no TUI).
func (a *App) delegationView(id, role string) *tui.ChatViewport {
	v := a.ensureDelegationView(id, role)
	if v == nil {
		return nil
	}
	return v.Transcript.View()
}

// delegationLabel is the display label for a delegation's stream blocks —
// the same shape the tab strip uses (coder·dlg-03).
func delegationLabel(delegationID string) string {
	return agentctx.TabLabel(delegationID)
}

// handleDelegationThinking appends a thinking chunk (or reconciles the full
// thinking text) inside the delegation's own transcript.
func (a *App) handleDelegationThinking(id, text string, isDelta bool) {
	if a.subs.cfg != nil && !a.subs.cfg.TUI.Transparency.ShowThinking {
		return
	}
	view := a.delegationView(id, "")
	if view == nil {
		return
	}
	state := a.delegationStreams().get(id)
	if state.kind != tui.ConsoleThinkingBlock && state.kind != 0 {
		state.endSegment()
	}
	if isDelta {
		state.thinking.WriteString(text)
	} else {
		state.thinking.Reset()
		state.thinking.WriteString(text)
	}
	label := delegationLabel(id)
	if state.thinkView == nil {
		expanded := a.subs.cfg == nil || !a.subs.cfg.TUI.Transparency.ThinkingCollapsed
		state.thinkView = view.AddAgentThinkingBlock(label, state.thinking.String(), expanded)
		state.kind = tui.ConsoleThinkingBlock
	} else {
		view.UpdateAgentThinking(label, state.thinking.String())
	}
	a.requestDelegationRender(id)
}

// handleDelegationContent appends a content chunk (or reconciles the full
// message text) inside the delegation's own transcript.
func (a *App) handleDelegationContent(id, text string, isDelta bool) {
	view := a.delegationView(id, "")
	if view == nil {
		return
	}
	state := a.delegationStreams().get(id)
	if state.kind != tui.ConsoleAgentMessage && state.kind != 0 {
		state.endSegment()
	}
	if isDelta {
		state.content.WriteString(text)
	} else {
		state.content.Reset()
		state.content.WriteString(text)
	}
	label := delegationLabel(id)
	if state.contentView == nil {
		state.contentView = view.AddAgentContent(label, state.content.String())
		state.kind = tui.ConsoleAgentMessage
	} else {
		view.UpdateAgentContent(label, state.content.String())
	}
	a.requestDelegationRender(id)
}

// handleDelegationFinished marks the delegation terminal: the stream state is
// closed, a terminal marker lands in the delegation's transcript (the FAILED
// error card on failure), and the registry badge state records the outcome so
// the tab stays marked until viewed.
func (a *App) handleDelegationFinished(id, role, status, detail string) {
	a.delegationStreams().end(id)
	a.setDelegationStatus(id, status)
	reg := a.subs.agentRegistry
	view := a.delegationView(id, role)
	if view == nil || reg == nil {
		return
	}
	label := delegationLabel(id)
	if status == "failed" {
		// Bug-2 fix: the failure is always visible — a marked tab (registry
		// error state, rendered as ▲ from T5) plus an error card inside the
		// delegation's transcript, plus a flash on the main view so the user
		// sees it without opening the tab.
		view.AddSystemMessage(fmt.Sprintf("✗ Delegation %s FAILED: %s", label, detail))
		reg.MarkError(id)
		a.flashDelegationFailure(label, detail)
	} else {
		view.AddSystemMessage(fmt.Sprintf("— delegation %s %s —", label, status))
		reg.MarkActivity(id)
	}
	a.requestDelegationRender(id)
}

// flashDelegationFailure surfaces a delegation failure on whatever tab the
// user is currently viewing, via the chat event bus (non-blocking: dropped
// when no consumer is attached, e.g. headless tests).
func (a *App) flashDelegationFailure(label, detail string) {
	if a.subs.events == nil {
		return
	}
	select {
	case a.subs.events.Chat <- event.ChatEvent{Flash: &event.Flash{
		Text: fmt.Sprintf("Delegation %s FAILED: %s", label, detail),
	}}:
	default:
	}
}

// requestDelegationRender records background activity and asks the engine for
// a frame. When the delegation's tab is inactive the append was pure data and
// only the tab strip changes; when active the transcript repaints. The
// active-tab chrome (prompt label + footer stats, T5) is refreshed alongside:
// block counts grow per chunk and lifecycle edges flip the steer target.
func (a *App) requestDelegationRender(id string) {
	if reg := a.subs.agentRegistry; reg != nil {
		if activeID, _ := reg.Active(); activeID != id {
			reg.MarkActivity(id)
		}
	}
	a.refreshAgentCtxChrome()
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.RequestRender()
	}
}
