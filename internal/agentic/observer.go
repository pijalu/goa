// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import "fmt"

// OutputState represents the current activity state of the agent output.
type OutputState int

const (
	// StateIdle indicates no active output.
	StateIdle OutputState = iota
	// StateThinking indicates the LLM is generating thinking/reasoning tokens.
	StateThinking
	// StateContent indicates the LLM is generating content tokens.
	StateContent
	// StateToolResult indicates a tool result is being emitted.
	StateToolResult
	// StateToolCall indicates the LLM is requesting a tool call.
	StateToolCall
)

// EventType categorizes the different kinds of output events.
type EventType string

const (
	// EventStateChange indicates a transition between OutputStates.
	EventStateChange EventType = "state_change"
	// EventContent indicates text content (from LLM or tool result).
	EventContent EventType = "content"
	// EventToolCall indicates the LLM requested a tool execution.
	EventToolCall EventType = "tool_call"
	// EventToolResult indicates a tool execution completed.
	EventToolResult EventType = "tool_result"
	// EventToolProgress carries partial output emitted by a tool while it is
	// still running (e.g. streamed stdout of a long bash command). It is a
	// transient UI update: it does not complete the tool call and is not sent
	// to the model.
	EventToolProgress EventType = "tool_progress"
	// EventEnd signals the end of a conversation turn.
	EventEnd EventType = "end"
	// EventClear signals the conversation was cleared.
	EventClear EventType = "clear"
	// EventCompact signals the conversation was compacted.
	EventCompact EventType = "compact"
	// EventTokenStats carries token generation statistics.
	EventTokenStats EventType = "token_stats"
	// EventProgress carries prompt processing progress.
	EventProgress EventType = "progress"
	// EventContextStats carries context window usage statistics.
	EventContextStats EventType = "context_stats"
)

// TokenTimings holds performance metrics from the LLM inference.
type TokenTimings struct {
	PromptN            int     `json:"prompt_n"`
	PredictedN         int     `json:"predicted_n"`
	PromptMs           float64 `json:"prompt_ms"`
	PredictedMs        float64 `json:"predicted_ms"`
	PromptPerSecond    float64 `json:"prompt_per_second"`
	PredictedPerSecond float64 `json:"predicted_per_second"`
	CacheReadTokens    int     `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens   int     `json:"cache_write_tokens,omitempty"`
}

// PromptProgress tracks the progress of prompt processing.
type PromptProgress struct {
	Total     int `json:"total"`
	Cache     int `json:"cache"`
	Processed int `json:"processed"`
	TimeMs    int `json:"time_ms"`
}

// OutputEvent is the unified event type broadcast to all observers.
// The Type field determines which other fields are populated.
type OutputEvent struct {
	Type           EventType
	State          OutputState
	Role           Role
	Text           string
	IsDelta        bool
	ToolName       string
	ToolInput      string
	ToolCallID     string
	ToolResult     string
	Timings        *TokenTimings   `json:"timings,omitempty"`
	PromptProgress *PromptProgress `json:"prompt_progress,omitempty"`

	// ContextStats carries context window usage when Type is EventContextStats.
	ContextStats *ContextStats `json:"context_stats,omitempty"`

	// Metadata is a set of opaque key/value strings attached to the event.
	// It is NOT sent to the LLM, but is propagated through the observer
	// pipeline (including the XML stream). Clients use this to store
	// application-level tags such as category or visibility flags.
	Metadata map[string]string `json:"metadata,omitempty"`
}

// OutputObserver receives output events from an Agent.
// Implementations can handle events for UI updates, logging, or custom processing.
type OutputObserver interface {
	OnEvent(event OutputEvent)
}

// OutputObserverFunc is an adapter to allow the use of ordinary functions as
// OutputObservers. If f is a function with the appropriate signature,
// OutputObserverFunc(f) is an OutputObserver that calls f.
type OutputObserverFunc func(event OutputEvent)

// OnEvent calls f(event).
func (f OutputObserverFunc) OnEvent(event OutputEvent) { f(event) }

// String returns a human-readable representation of the state.
func (s OutputState) String() string {
	switch s {
	case StateIdle:
		return "idle"
	case StateThinking:
		return "thinking"
	case StateContent:
		return "content"
	case StateToolResult:
		return "tool_result"
	case StateToolCall:
		return "tool_call"
	default:
		return fmt.Sprintf("unknown(%d)", s)
	}
}
