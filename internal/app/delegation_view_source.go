// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"github.com/pijalu/goa/multiagent"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
)

// translateDelegationMsg is the delegation-source adapter: it converts one
// multiagent.OrchestratorMessage into the neutral orchpanel.AgentViewEvent the
// source-agnostic MultiAgentView consumes. It is DISTINCT from
// translateOrchEvent — that one serves the core/orchestrator runtime's
// orchestrator.Event; this one serves the ForegroundOrchestrator's
// delegation/stream messages. Adding a source = adding an adapter, never
// touching the view (Open/Closed).
//
// Every produced event copies DelegationID / From (→Role) through so the view
// can key tabs/logs per delegation, distinguishing concurrent same-role
// delegations. Returns ok=false for kinds that carry no per-agent viewable
// payload (control messages, stream framing) or unknown kinds.
func translateDelegationMsg(msg multiagent.OrchestratorMessage) (orchpanel.AgentViewEvent, bool) {
	base := orchpanel.AgentViewEvent{
		AgentID:      msg.From, // delegation source keys agents by role name
		Role:         msg.From,
		DelegationID: msg.DelegationID,
	}
	switch msg.Kind {
	case "thinking_chunk":
		base.Kind = orchpanel.EvAgentThinking
		base.Text = msg.Content
		base.IsDelta = true
		return base, true
	case "content":
		return translateDelegationContent(msg, base)
	}
	// thinking_start / thinking_end / stream framing / control / unknown kinds
	// carry no chunk the view renders — not translatable.
	return orchpanel.AgentViewEvent{}, false
}

// translateDelegationContent maps a "content"-kind OrchestratorMessage. Only
// the streamed chunk (To == "stream_chunk") carries renderable text; the
// stream_start/stream_end markers are framing and map to ok=false so the view
// is not driven by empty events.
func translateDelegationContent(msg multiagent.OrchestratorMessage, base orchpanel.AgentViewEvent) (orchpanel.AgentViewEvent, bool) {
	if msg.To != "stream_chunk" {
		return orchpanel.AgentViewEvent{}, false
	}
	base.Kind = orchpanel.EvAgentMessage
	base.Text = msg.Content
	base.IsDelta = true
	return base, true
}
