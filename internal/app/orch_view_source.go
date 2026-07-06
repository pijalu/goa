// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"github.com/pijalu/goa/core/orchestrator"
	orchpanel "github.com/pijalu/goa/tui/orchestrator"
)

// translateOrchEvent is the ONLY orchestration-specific seam: it converts one
// core/orchestrator.Event into a neutral orchpanel.AgentViewEvent that the
// source-agnostic MultiAgentView understands. The forwarder (not the view)
// knows about orchestrator.Event; the view never imports core/orchestrator.
//
// Adding a new multi-agent source (pipeline, swarm, …) later means duplicating
// this single function per source — the view itself stays untouched (Open/Closed).
// Returns ok=false for event kinds the view does not care about.
func translateOrchEvent(ev orchestrator.Event) (orchpanel.AgentViewEvent, bool) {
	switch ev.Type {
	case orchestrator.EventRunStarted:
		return orchpanel.AgentViewEvent{Kind: orchpanel.EvSourceStarted, Meta: orchRunMeta(ev)}, true
	case orchestrator.EventRunFinished:
		status := "ok"
		if ok, _ := ev.Payload["ok"].(bool); !ok {
			status = "failed"
		}
		return orchpanel.AgentViewEvent{Kind: orchpanel.EvSourceFinished, Status: status}, true
	case orchestrator.EventAgentStarted:
		return orchpanel.AgentViewEvent{
			Kind:     orchpanel.EvAgentStarted,
			AgentID:  ev.AgentID,
			Role:     ev.Role,
			Model:    ev.Model,
			Provider: orchStr(ev.Payload, "provider"),
			Thinking: orchStr(ev.Payload, "thinking"),
		}, true
	case orchestrator.EventAgentMessage:
		return orchpanel.AgentViewEvent{
			Kind:    orchpanel.EvAgentMessage,
			AgentID: ev.AgentID, Role: ev.Role,
			Text: orchStr(ev.Payload, "text"),
		}, true
	case orchestrator.EventAgentStats:
		return orchpanel.AgentViewEvent{
			Kind:     orchpanel.EvAgentStats,
			AgentID:  ev.AgentID, Role: ev.Role,
			Status:   orchStr(ev.Payload, "status"),
			Thinking: orchStr(ev.Payload, "thinking"),
			Stats: &orchpanel.AgentStatsDelta{
				Turns:         orchInt(ev.Payload, "turns"),
				TokensIn:      orchInt(ev.Payload, "tokens_in"),
				TokensOut:     orchInt(ev.Payload, "tokens_out"),
				CacheRead:     orchInt(ev.Payload, "cache_read"),
				CacheCreation: orchInt(ev.Payload, "cache_creation"),
				ToolCalls:     orchInt(ev.Payload, "tool_calls"),
			},
		}, true
	case orchestrator.EventAgentSteered:
		return orchpanel.AgentViewEvent{
			Kind:    orchpanel.EvAgentSteered,
			AgentID: ev.AgentID, Role: ev.Role,
			Text: orchStr(ev.Payload, "text"),
		}, true
	case orchestrator.EventAgentFinished:
		return orchpanel.AgentViewEvent{
			Kind:    orchpanel.EvAgentFinished,
			AgentID: ev.AgentID, Role: ev.Role,
			Status: orchFinishedStatus(ev.Payload),
		}, true
	}
	return orchpanel.AgentViewEvent{}, false
}

// orchRunMeta copies display-only run metadata from the run_started payload.
func orchRunMeta(ev orchestrator.Event) map[string]string {
	meta := map[string]string{}
	for _, k := range []string{"objective", "topology", "name", "goal_id"} {
		if s := orchStr(ev.Payload, k); s != "" {
			meta[k] = s
		}
	}
	return meta
}

// orchFinishedStatus derives the agent's terminal status label for the view.
func orchFinishedStatus(p map[string]any) string {
	if s := orchStr(p, "outcome"); s != "" {
		return s
	}
	if s := orchStr(p, "status"); s != "" {
		return s
	}
	return "finished"
}

// orchStr reads a string payload field (empty when absent/wrong type).
func orchStr(p map[string]any, k string) string {
	if v, ok := p[k].(string); ok {
		return v
	}
	return ""
}

// orchInt reads an int payload field (0 when absent/wrong type).
func orchInt(p map[string]any, k string) int {
	switch n := p[k].(type) {
	case int:
		return n
	case int64:
		return int(n)
	case float64:
		return int(n)
	}
	return 0
}
