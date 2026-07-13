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
		return translateRunFinished(ev)
	case orchestrator.EventAgentStarted:
		return translateAgentStarted(ev), true
	case orchestrator.EventAgentMessage:
		return translateAgentMessage(ev), true
	case orchestrator.EventAgentThinking:
		return translateAgentThinking(ev), true
	case orchestrator.EventAgentToolCall:
		return translateAgentToolCall(ev), true
	case orchestrator.EventAgentToolResult:
		return translateAgentToolResult(ev), true
	case orchestrator.EventAgentStats:
		return translateAgentStats(ev), true
	case orchestrator.EventAgentSteered:
		return translateAgentSteered(ev), true
	case orchestrator.EventAgentFinished:
		return translateAgentFinished(ev), true
	case orchestrator.EventAskUser:
		return translateAskUser(ev), true
	}
	return orchpanel.AgentViewEvent{}, false
}

func translateRunFinished(ev orchestrator.Event) (orchpanel.AgentViewEvent, bool) {
	status := "ok"
	if ok, _ := ev.Payload["ok"].(bool); !ok {
		status = "failed"
	}
	return orchpanel.AgentViewEvent{Kind: orchpanel.EvSourceFinished, Status: status}, true
}

func translateAgentStarted(ev orchestrator.Event) orchpanel.AgentViewEvent {
	return orchpanel.AgentViewEvent{
		Kind:     orchpanel.EvAgentStarted,
		AgentID:  ev.AgentID,
		Role:     ev.Role,
		Model:    ev.Model,
		Provider: orchStr(ev.Payload, "provider"),
		Thinking: orchStr(ev.Payload, "thinking"),
	}
}

func translateAgentMessage(ev orchestrator.Event) orchpanel.AgentViewEvent {
	return orchpanel.AgentViewEvent{
		Kind:    orchpanel.EvAgentMessage,
		AgentID: ev.AgentID, Role: ev.Role,
		Text: orchStr(ev.Payload, "text"),
	}
}

func translateAgentThinking(ev orchestrator.Event) orchpanel.AgentViewEvent {
	return orchpanel.AgentViewEvent{
		Kind:    orchpanel.EvAgentThinking,
		AgentID: ev.AgentID, Role: ev.Role,
		Text: orchStr(ev.Payload, "text"),
	}
}

func translateAgentToolCall(ev orchestrator.Event) orchpanel.AgentViewEvent {
	return orchpanel.AgentViewEvent{
		Kind:      orchpanel.EvAgentToolCall,
		AgentID:   ev.AgentID, Role: ev.Role,
		Tool:      orchStr(ev.Payload, "tool"),
		ToolInput: orchStr(ev.Payload, "input"),
		CallID:    orchStr(ev.Payload, "call_id"),
		IsDelta:   orchBool(ev.Payload, "is_delta"),
	}
}

func translateAgentToolResult(ev orchestrator.Event) orchpanel.AgentViewEvent {
	return orchpanel.AgentViewEvent{
		Kind:    orchpanel.EvAgentToolResult,
		AgentID: ev.AgentID, Role: ev.Role,
		CallID: orchStr(ev.Payload, "call_id"),
		Text:   orchStr(ev.Payload, "text"),
		OK:     orchBool(ev.Payload, "ok"),
	}
}

func translateAgentStats(ev orchestrator.Event) orchpanel.AgentViewEvent {
	return orchpanel.AgentViewEvent{
		Kind:     orchpanel.EvAgentStats,
		AgentID:  ev.AgentID, Role: ev.Role,
		Status:   orchStr(ev.Payload, "status"),
		Thinking: orchStr(ev.Payload, "thinking"),
		Stats: &orchpanel.AgentStatsDelta{
			Turns:           orchInt(ev.Payload, "turns"),
			TokensIn:        orchInt(ev.Payload, "tokens_in"),
			TokensOut:       orchInt(ev.Payload, "tokens_out"),
			CacheRead:       orchInt(ev.Payload, "cache_read"),
			CacheCreation:   orchInt(ev.Payload, "cache_creation"),
			ToolCalls:       orchInt(ev.Payload, "tool_calls"),
			ContextEstimate: orchInt(ev.Payload, "context_estimate"),
			ContextMax:      orchInt(ev.Payload, "context_max"),
			ContextAutoMax:  orchBool(ev.Payload, "context_auto_max"),
		},
	}
}

func translateAgentSteered(ev orchestrator.Event) orchpanel.AgentViewEvent {
	return orchpanel.AgentViewEvent{
		Kind:    orchpanel.EvAgentSteered,
		AgentID: ev.AgentID, Role: ev.Role,
		Text: orchStr(ev.Payload, "text"),
	}
}

func translateAgentFinished(ev orchestrator.Event) orchpanel.AgentViewEvent {
	return orchpanel.AgentViewEvent{
		Kind:    orchpanel.EvAgentFinished,
		AgentID: ev.AgentID, Role: ev.Role,
		Status: orchFinishedStatus(ev.Payload),
		Text:   orchStr(ev.Payload, "text"),
	}
}

func translateAskUser(ev orchestrator.Event) orchpanel.AgentViewEvent {
	return orchpanel.AgentViewEvent{
		Kind:     orchpanel.EvAskUser,
		AgentID:  ev.AgentID, Role: ev.Role,
		Question: orchStr(ev.Payload, "question"),
	}
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

// orchBool reads a bool payload field (false when absent/wrong type).
func orchBool(p map[string]any, k string) bool {
	if v, ok := p[k].(bool); ok {
		return v
	}
	return false
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
