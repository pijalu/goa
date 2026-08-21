// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"strings"

	"github.com/pijalu/goa/multiagent"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
)

// translateDelegationMsg is the delegation-source adapter: it converts one
// multiagent.OrchestratorMessage into the neutral orchpanel.AgentViewEvent the
// source-agnostic per-delegation routing consumes. It is DISTINCT from
// translateOrchEvent — that one serves the core/orchestrator runtime's
// orchestrator.Event; this one serves the ForegroundOrchestrator's
// delegation/stream messages. Adding a source = adding an adapter, never
// touching the view (Open/Closed).
//
// Every produced event copies DelegationID / From (→Role) through so the
// router can key transcripts per delegation, distinguishing concurrent
// same-role delegations. Returns ok=false for kinds that carry no per-agent
// viewable payload (control messages, stream framing) or unknown kinds.
//
// T4 additions:
//   - delegation_state → EvAgentStarted (running) / EvAgentFinished
//     (completed|failed, Status + Text=detail) so a tab appears the moment a
//     delegation is created and its terminal state marks it (bug-2 fix).
//   - stream_end / thinking_end → full-text non-delta events: the chunk
//     fanout is lossy (emitKind drops stream_chunk under pressure), so the
//     terminal framing messages carry the authoritative full text the
//     transcript reconciles against.
func translateDelegationMsg(msg multiagent.OrchestratorMessage) (orchpanel.AgentViewEvent, bool) {
	base := orchpanel.AgentViewEvent{
		AgentID:      msg.From, // delegation source keys agents by role name
		Role:         msg.From,
		DelegationID: msg.DelegationID,
	}
	switch msg.Kind {
	case "delegation_state":
		return translateDelegationState(msg, base)
	case "thinking_chunk":
		base.Kind = orchpanel.EvAgentThinking
		base.Text = msg.Content
		base.IsDelta = true
		return base, true
	case "thinking_end":
		base.Kind = orchpanel.EvAgentThinking
		base.Text = msg.Content
		return base, true
	case "content":
		return translateDelegationContent(msg, base)
	}
	// thinking_start / stream_start framing / control / unknown kinds carry
	// no chunk the view renders — not translatable.
	return orchpanel.AgentViewEvent{}, false
}

// translateDelegationState maps a delegation lifecycle message
// (Content "<state>|<detail>", see multiagent.EmitDelegationState) to the
// neutral agent lifecycle events: running → EvAgentStarted; completed/failed
// → EvAgentFinished with Status (and the error detail in Text for failures).
func translateDelegationState(msg multiagent.OrchestratorMessage, base orchpanel.AgentViewEvent) (orchpanel.AgentViewEvent, bool) {
	state, detail, _ := strings.Cut(msg.Content, "|")
	switch state {
	case multiagent.DelegationRunning:
		base.Kind = orchpanel.EvAgentStarted
		return base, true
	case multiagent.DelegationCompleted, multiagent.DelegationFailed:
		base.Kind = orchpanel.EvAgentFinished
		base.Status = state
		base.Text = detail
		return base, true
	}
	return orchpanel.AgentViewEvent{}, false
}

// translateDelegationContent maps a "content"-kind OrchestratorMessage. The
// streamed chunk (To == "stream_chunk") is a delta; the terminal stream_end
// carries the authoritative full text as a non-delta reconcile event (the
// chunk fanout is lossy under back-pressure). stream_start is pure framing
// and maps to ok=false so the view is not driven by empty events.
func translateDelegationContent(msg multiagent.OrchestratorMessage, base orchpanel.AgentViewEvent) (orchpanel.AgentViewEvent, bool) {
	switch msg.To {
	case "stream_chunk":
		base.Kind = orchpanel.EvAgentMessage
		base.Text = msg.Content
		base.IsDelta = true
		return base, true
	case "stream_end":
		base.Kind = orchpanel.EvAgentMessage
		base.Text = msg.Content
		return base, true
	}
	return orchpanel.AgentViewEvent{}, false
}
