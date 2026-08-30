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
	// EventToolStart signals a queued tool call actually began executing
	// (the scheduler started its task). The UI flips the widget from the
	// "waiting" state (⧖, queued behind conflicting/earlier calls) to the
	// "elapsed" state at this moment — NOT at args-complete — so a queued
	// call's timer measures execution only (Bug W). It is transient
	// UI state: not sent to the model, not persisted to history.
	EventToolStart EventType = "tool_start"
	// EventEnd signals the end of a conversation turn.
	EventEnd EventType = "end"
	// EventClear signals the conversation was cleared.
	EventClear EventType = "clear"
	// EventContextReset signals the live conversation context was reset in
	// place to a cold start — currently a fresh-context goal begin
	// (FreshAgentRunner.RunFresh): history holds only the system prompt and
	// the provider cache key was rotated. Unlike EventClear (user /clear),
	// session-level counters must NOT be wiped: only per-conversation
	// detector baselines (e.g. the cache-bust detector) re-arm, so the new
	// conversation's cold start is not miscounted as a cache bust.
	EventContextReset EventType = "context_reset"
	// EventCompact signals the conversation was compacted.
	EventCompact EventType = "compact"
	// EventTokenStats carries token generation statistics.
	EventTokenStats EventType = "token_stats"
	// EventProgress carries prompt processing progress.
	EventProgress EventType = "progress"
	// EventContextStats carries context window usage statistics.
	EventContextStats EventType = "context_stats"
	// EventRateLimit signals a classified LLM stream failure (plan §6,
	// Phase M5). Emitted only on failure paths — once per scheduled retry
	// (WillRetry=true) and once when the episode gives up (WillRetry=false);
	// clean turns never emit it. The payload rides in RateLimit.
	EventRateLimit EventType = "rate_limit"
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

// CompactionInfo describes a completed compression pass. It is attached to an
// EventCompact OutputEvent so every visible surface (conversation bubble,
// footer counter, session JSONL) can render/count/document the pass from a
// single structured payload instead of inferring it from a free-text label.
type CompactionInfo struct {
	// Strategy names the compression pass that ran:
	// elision|selective|micro|summarize|hybrid|ceiling|overflow|truncation.
	Strategy string `json:"strategy"`
	// BeforePct is the context usage percent before the pass.
	BeforePct int `json:"before_pct"`
	// AfterPct is the context usage percent after the pass.
	AfterPct int `json:"after_pct"`
	// FreedTokens is the estimated number of tokens freed (0 = unknown).
	FreedTokens int `json:"freed_tokens,omitempty"`
	// Removed is the number of messages dropped (0 = none / in-place edit).
	Removed int `json:"removed,omitempty"`
	// Detail carries the summarize summary text for CompressionSummarize,
	// and is empty for all other strategies.
	Detail string `json:"detail,omitempty"`
}

// RateLimitInfo describes one classified LLM stream failure (plan §6, Phase
// M5). It is attached to an EventRateLimit OutputEvent so downstream surfaces
// (the plugin event bus → quota-plugin hint) can react without parsing error
// text.
//
// Emission semantics: one event per SCHEDULED retry carries WillRetry=true
// with Attempt set to the scheduled retry index and RetryAfterMS to the
// actual backoff delay (server Retry-After honored when present). When the
// episode gives up — non-retryable classification or exhausted budget — a
// single terminal event with WillRetry=false is emitted; its Attempt is 0,
// so consumers derive the attempt count by counting the preceding
// WillRetry=true events. Clean turns never emit.
type RateLimitInfo struct {
	// Provider is the provider id the failing request targeted ("openai",
	// "anthropic", "custom", ...).
	Provider string `json:"provider,omitempty"`
	// Model is the model ID that failed.
	Model string `json:"model"`
	// Attempt is the scheduled retry index for WillRetry=true events
	// (0-based); 0 on terminal events.
	Attempt int `json:"attempt"`
	// RetryAfterMS is the scheduled backoff delay in milliseconds (0 when
	// unknown or terminal). Server-supplied Retry-After values are honored
	// here when the backoff computation used them.
	RetryAfterMS int64 `json:"retry_after_ms,omitempty"`
	// Classified is the canonical retry code (RATE_LIMIT, SERVER, TIMEOUT,
	// TRANSPORT, EMPTY_RESPONSE) or UNKNOWN when the classifier has no
	// recognized code for the error.
	Classified string `json:"classified"`
	// WillRetry reports whether another attempt was scheduled. False marks
	// the terminal event of a failure episode.
	WillRetry bool `json:"will_retry"`
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

	// Compaction carries the structured compression-pass record when Type is
	// EventCompact. It is nil for EventCompact events emitted by code paths
	// that predate the structured payload (treated as a bare signal).
	Compaction *CompactionInfo `json:"compaction,omitempty"`

	// CompactionTx carries the durable compaction-transaction record when
	// Type is EventCompactionStart, EventCompactionSummary or
	// EventCompactionEnd (CX4 provenance triple). It is nil for every other
	// event type.
	CompactionTx *CompactionTx `json:"compaction_tx,omitempty"`

	// RateLimit carries the classified stream-failure record when Type is
	// EventRateLimit (plan §6). It is nil for every other event type.
	RateLimit *RateLimitInfo `json:"rate_limit,omitempty"`

	// Metadata is a set of opaque key/value strings attached to the event.
	// It is NOT sent to the LLM, but is propagated through the observer
	// pipeline (including the XML stream). Clients use this to store
	// application-level tags such as category or visibility flags.
	Metadata map[string]string `json:"metadata,omitempty"`

	// TextOnlyCollapse marks an EventTokenStats emitted for a stream round
	// that ran with the P7 text-only collapse (request carried no tools and
	// tool_choice "none"): the round's provider-prefix bust is an
	// intentional request-shape change, so /stats:cache classifies it as a
	// "no-tools step" instead of an unexpected miss (bugs.md 2026-08-30).
	// Always false for every other event type.
	TextOnlyCollapse bool
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
