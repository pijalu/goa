// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	orchpanel "github.com/pijalu/goa/tui/orchestrator"

	"github.com/pijalu/goa/core/orchestrator"
)

// orchEventSource is the runtime surface the view forwarder depends on. It is
// satisfied by *orchestrator.Runtime and by test fakes, so the forwarder logic
// is unit-testable without a live model (the orchestrator.Event type is the
// only thing crossing this seam).
type orchEventSource interface {
	Subscribe() <-chan orchestrator.Event
	Done() <-chan struct{}
}

// runOrchestratorViewForwarder is the single owner of the persistent tabbed
// multi-agent run view. It watches the active-runtime holder; whenever a new run
// becomes active it attaches a fresh MultiAgentView to the AgentContent and
// AgentTabBar components and drains that run's events ON THE COMMAND LOOP (via
// a.apply) so view state is mutated only by the loop — preserving the R1
// single-owner invariant. Unlike the old panel overlay, the view is PERSISTENT:
// it stays after the run finishes until a new run replaces it.
func (a *App) runOrchestratorViewForwarder(done <-chan struct{}) {
	for {
		select {
		case <-done:
			return
		case <-a.subs.orchActive.Notify():
			src := a.subs.orchActive.Get()
			if src == nil {
				continue
			}
			a.attachOrchView(src)
			a.drainOrchView(done, src)
		}
	}
}

// attachOrchView creates the view for a new run and binds the render-only
// components. The default active tab is Conversation, so the chat viewport is
// visible and the AgentContent region is hidden. Ctrl+x toggles to Stats,
// which suppresses the chat and shows the stats panel.
func (a *App) attachOrchView(src orchEventSource) {
	view := orchpanel.NewMultiAgentView("orchestration")
	a.apply(func() {
		a.subs.agentView = view
		if a.subs.agentContent != nil {
			a.subs.agentContent.SetView(view)
		}
		if a.subs.agentTabBar != nil {
			a.subs.agentTabBar.SetView(view)
		}
		if a.subs.chat != nil {
			// Default to Stats: the persistent stats panel is visible and the
			// chat viewport is suppressed until the user switches to Conversation.
			a.subs.chat.SetSuppressed(true)
		}
		// Fresh stream registry per run, unconditionally. (The previous
		// `if != nil` guard was inverted: it only reset when already set, and
		// would have left a nil registry — rendering nothing — if the
		// pre-init ever changed.)
		a.subs.agentStreams = newAgentStreamRegistry()
		a.updateOrchInputPrompt()
	})
}

// drainOrchView translates each orchestrator event into a neutral
// AgentViewEvent and applies it on the command loop. It returns when the run
// finishes (leaving the view attached and persistent) or when the app is
// stopping.
func (a *App) drainOrchView(done <-chan struct{}, src orchEventSource) {
	sub := src.Subscribe()
	for {
		select {
		case <-done:
			return
		case <-src.Done():
			// The view is persistent: leave it attached so the user can read
			// the final stats/transcript. A new run (attachOrchView) resets it.
			return
		case ev := <-sub:
			ne, ok := translateOrchEvent(ev)
			if !ok {
				continue
			}
			a.apply(func() {
				a.handleOrchViewEvent(ne)
				a.updateOrchInputPrompt()
			})
		}
	}
}

// handleOrchViewEvent routes a neutral agent view event to either the chat
// viewport (conversation streaming) or the persistent stats panel (lifecycle,
// stats, steering). It must be called on the command loop.
func (a *App) handleOrchViewEvent(ne orchpanel.AgentViewEvent) {
	switch ne.Kind {
	case orchpanel.EvAgentThinking:
		a.handleAgentThinking(ne.AgentID, ne.Text)
	case orchpanel.EvAgentMessage:
		a.handleAgentContent(ne.AgentID, ne.Text)
	case orchpanel.EvAgentToolCall:
		a.handleAgentToolCall(ne.AgentID, ne.Tool, ne.ToolInput, ne.CallID)
	case orchpanel.EvAgentToolResult:
		a.handleAgentToolResult(ne.AgentID, ne.CallID, ne.Text, ne.OK)
	case orchpanel.EvAgentStarted:
		a.applyAgentStarted(ne)
	case orchpanel.EvAgentFinished:
		a.applyAgentFinished(ne)
	case orchpanel.EvSourceFinished:
		a.applySourceFinished(ne)
	default:
		a.applyToView(ne)
	}
}

// applyAgentStarted begins the agent's stream and forwards the event to the
// persistent view.
func (a *App) applyAgentStarted(ne orchpanel.AgentViewEvent) {
	a.beginAgentStream(ne.Role, ne.AgentID)
	a.applyToView(ne)
}

// applyAgentFinished reconciles the agent's final content, forwards the event,
// and ends the stream.
func (a *App) applyAgentFinished(ne orchpanel.AgentViewEvent) {
	if ne.Text != "" {
		a.reconcileAgentContent(ne.AgentID, ne.Text)
	}
	a.applyToView(ne)
	a.endAgentStream(ne.AgentID)
}

// applySourceFinished clears the shared status spinner so it does not linger
// (e.g. "orchestrator answering...") after the finish banner appears, then
// forwards the event. EvSourceFinished is the last event of a run; EvAgentStats
// (the only event that may follow) does not touch the spinner, so a plain
// Clear() suffices — no SessionEnd guard (the main-agent path owns that
// pairing via submithandler).
func (a *App) applySourceFinished(ne orchpanel.AgentViewEvent) {
	if a.subs.statusMsg != nil {
		a.subs.statusMsg.Clear()
	}
	a.applyToView(ne)
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.RequestRender()
	}
}

// applyToView forwards an event to the persistent agent view (no-op when no
// view is attached).
func (a *App) applyToView(ne orchpanel.AgentViewEvent) {
	if a.subs.agentView != nil {
		a.subs.agentView.ApplyEvent(ne)
	}
}
