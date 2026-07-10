// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package orchestrator

// This file defines the neutral agent-event seam that the persistent
// multi-agent run view consumes. It deliberately imports NOTHING from
// core/orchestrator: every multi-agent source (orchestrator runtime,
// foreground orchestrator, pipeline, swarm) feeds the SAME view by translating
// its own events into these neutral shapes. Keeping the seam neutral is what
// makes the planned `tui/multiagent` generalization a rename, not a rewrite.

// AgentEventKind enumerates the lifecycle events a source can feed the view.
type AgentEventKind string

const (
	// EvSourceStarted is emitted when a multi-agent source (a run) begins.
	// Meta carries display-only source metadata (objective/topology/name).
	EvSourceStarted AgentEventKind = "source_started"
	// EvSourceFinished is emitted when the source completes. Status == "failed"
	// marks the run as failed; any other value is treated as success.
	EvSourceFinished AgentEventKind = "source_finished"
	// EvAgentStarted announces a new agent. Provider/Model/Thinking populate
	// the stats row; the agent's first appearance creates a dedicated tab.
	EvAgentStarted AgentEventKind = "agent_started"
	// EvAgentMessage carries a chunk of streamed agent text for its tab log.
	EvAgentMessage AgentEventKind = "agent_message"
	// EvAgentThinking carries a chunk of streamed agent thinking.
	EvAgentThinking AgentEventKind = "agent_thinking"
	// EvAgentToolCall carries a tool call the agent requested.
	EvAgentToolCall AgentEventKind = "agent_tool_call"
	// EvAgentToolResult carries a completed tool result for the agent.
	EvAgentToolResult AgentEventKind = "agent_tool_result"
	// EvAgentStats carries an updated usage snapshot for the agent's row.
	EvAgentStats AgentEventKind = "agent_stats"
	// EvAgentSteered records a steering injection for transparency.
	EvAgentSteered AgentEventKind = "agent_steered"
	// EvAgentFinished marks an agent terminal; Status carries the outcome.
	EvAgentFinished AgentEventKind = "agent_finished"
)

// AgentStatsDelta is the per-agent usage snapshot carried by EvAgentStats.
// Values are absolute (latest reported totals), not incremental.
type AgentStatsDelta struct {
	Turns           int
	TokensIn        int
	TokensOut       int
	CacheRead       int
	CacheCreation   int
	ToolCalls       int
	ContextEstimate int
	ContextMax      int
	ContextAutoMax  bool
}

// AgentViewEvent is the single neutral type any multi-agent source translates
// INTO before feeding the view. No field references orchestration concepts:
// source identity and run-level metadata travel in Meta as free-form strings.
type AgentViewEvent struct {
	Kind      AgentEventKind
	AgentID   string
	Role      string
	Provider  string
	Model     string
	Thinking  string
	Status    string
	Text      string
	Tool      string
	ToolInput string
	CallID    string
	OK        bool
	Stats     *AgentStatsDelta
	Meta      map[string]string
}
