// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"

	orchpanel "github.com/pijalu/goa/tui/orchestrator"

	"github.com/pijalu/goa/core/orchestrator"
	"github.com/pijalu/goa/internal/agentic"
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

// attachOrchView creates the view for a new run and wires it to the chat
// viewport and footer. The chat is always visible in the simplified UI; stats
// are shown in the footer below the input line.
func (a *App) attachOrchView(src orchEventSource) {
	view := orchpanel.NewMultiAgentView("orchestration")
	a.apply(func() {
		a.subs.agentView = view
		if a.subs.chat != nil {
			// The chat is the single persistent view; never suppress it.
			a.subs.chat.SetSuppressed(false)
		}
		// Fresh stream registry per run, unconditionally.
		a.subs.agentStreams = newAgentStreamRegistry()
		a.updateOrchInputPrompt()
		a.updateOrchFooterStats()
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
				a.updateOrchFooterStats()
			})
		}
	}
}

// handleOrchViewEvent routes a neutral agent view event to either the chat
// viewport (conversation streaming) or the persistent stats panel (lifecycle,
// stats, steering). It must be called on the command loop.
func (a *App) handleOrchViewEvent(ne orchpanel.AgentViewEvent) {
	a.persistOrchViewEvent(ne)
	switch ne.Kind {
	case orchpanel.EvAgentThinking:
		a.handleAgentThinking(ne.AgentID, ne.Text, true)
	case orchpanel.EvAgentMessage:
		a.handleAgentContent(ne.AgentID, ne.Text, true)
	case orchpanel.EvAgentToolCall:
		a.handleAgentToolCall(ne.AgentID, ne.Tool, ne.ToolInput, ne.CallID, ne.IsDelta)
	case orchpanel.EvAgentToolResult:
		a.handleAgentToolResult(ne.AgentID, ne.CallID, ne.Text, ne.OK)
	case orchpanel.EvAgentStarted:
		a.applyAgentStarted(ne)
	case orchpanel.EvAgentFinished:
		a.applyAgentFinished(ne)
	case orchpanel.EvSourceFinished:
		a.applySourceFinished(ne)
	case orchpanel.EvAskUser:
		a.displayOrchestratorQuestion(ne)
	default:
		a.applyToView(ne)
	}
}

// persistOrchViewEvent writes sub-agent work to the session store so a saved
// session holds the FULL orchestration run — not just the bare /orchestrate
// command line (bugs.md item K: "orchestrate must log more — all sub agents
// work must exist in the session").
//
// Sub-agent turns are persisted as SYSTEM content tagged with the agent id,
// NOT as assistant turns: on restore, EventsToHistory folds assistant/tool
// events into the MAIN agent's conversation history, and sub-agent output is
// not the main agent's own words — injecting it there would poison the
// restored model context. System content is skipped by EventsToHistory (no
// history pollution) yet still replays into the chat transcript via
// ReplayAgentEvent, so a restored session shows every sub-agent's work.
//
// Session-store writes are local JSONL appends, not prompt bytes, so this is
// cache-hit-first neutral (guideline #9).
func (a *App) persistOrchViewEvent(ne orchpanel.AgentViewEvent) {
	store := a.subs.sessionStore
	if store == nil {
		return
	}
	text := orchLogText(ne)
	if text == "" {
		return
	}
	tag := ne.Role
	if tag == "" {
		tag = ne.AgentID
	}
	store.WriteEvent(agentic.OutputEvent{
		Type: agentic.EventContent,
		Role: agentic.System,
		Text: fmt.Sprintf("[%s] %s", tag, text),
	})
}

// orchLogText maps a sub-agent view event to the line persisted in the
// session store (empty string = nothing to persist for this event kind).
func orchLogText(ne orchpanel.AgentViewEvent) string {
	switch ne.Kind {
	case orchpanel.EvAgentMessage:
		return ne.Text
	case orchpanel.EvAgentThinking:
		return "(thinking) " + ne.Text
	case orchpanel.EvAgentToolCall:
		if ne.IsDelta {
			return ""
		}
		return fmt.Sprintf("tool %s %s", ne.Tool, ne.ToolInput)
	case orchpanel.EvAgentToolResult:
		return fmt.Sprintf("tool result (%s): %s", ne.Tool, truncateOrchLog(ne.Text))
	case orchpanel.EvAgentStarted:
		return fmt.Sprintf("— agent started (model=%s provider=%s) —", ne.Model, ne.Provider)
	case orchpanel.EvAgentFinished:
		return fmt.Sprintf("— agent finished (%s) —", ne.Status)
	case orchpanel.EvSourceStarted:
		return fmt.Sprintf("— run started: %s —", ne.Meta["objective"])
	case orchpanel.EvSourceFinished:
		return fmt.Sprintf("— run finished (%s) —", ne.Status)
	case orchpanel.EvAskUser:
		return "[orchestrator asks] " + ne.Question
	}
	return ""
}

// truncateOrchLog caps a persisted tool-result body so a huge result does not
// bloat the session file; the full text remains in the live view.
func truncateOrchLog(s string) string {
	const max = 4000
	if len(s) <= max {
		return s
	}
	return s[:max] + fmt.Sprintf("… (%d bytes elided)", len(s)-max)
}

// displayOrchestratorQuestion surfaces the orchestrator's ask_user question in
// the chat viewport so the user can see what they are being asked before the
// app blocks for input.
func (a *App) displayOrchestratorQuestion(ne orchpanel.AgentViewEvent) {
	if ne.Question == "" || a.subs.chat == nil {
		return
	}
	a.subs.chat.AddSystemMessage("[orchestrator asks] " + ne.Question)
	if a.subs.tuiEngine != nil {
		a.subs.tuiEngine.RequestRender()
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
