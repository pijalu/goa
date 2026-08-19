// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"strings"

	"github.com/pijalu/goa/internal/agentic/provider"
)

func (a *Agent) SetHistory(history []Message) {
	a.mu.Lock()
	defer a.mu.Unlock()

	// Ensure system prompt is preserved if not present in new history
	hasSystem := false
	for _, m := range history {
		if m.Role == System {
			hasSystem = true
			break
		}
	}

	if !hasSystem && a.cfg.SystemPrompt != "" {
		history = append([]Message{{
			Type:    Content,
			Role:    System,
			Content: a.cfg.SystemPrompt,
		}}, history...)
	}

	a.history = history
	a.cacheGeneration++
	// History was replaced wholesale (session restore): any recorded provider
	// prompt size belongs to the previous conversation, and the sticky dedup
	// state no longer reflects what's in history — re-persist on next turn
	// when the restored conversation lacks the current sticky set.
	a.lastPersistedSticky = ""
	a.invalidateContextUsageLocked()
}

// SetModel replaces the active model for subsequent turns without
// rebuilding the rest of the agent configuration.
func (a *Agent) SetModel(mdl provider.Model) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Model = mdl
	if mdl.ContextWindow > 0 {
		a.contextWindow.Store(int64(mdl.ContextWindow))
	}
}

// SetContextCompression replaces the context compression configuration for
// subsequent turns. Used when the model changes mid-session so the context
// ceiling tracks the new model's context window.
func (a *Agent) SetContextCompression(cfg ContextCompressionConfig) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.ContextCompression = cfg
}

// CompressionConfig returns the current context compression configuration.
func (a *Agent) CompressionConfig() ContextCompressionConfig {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.ContextCompression
}

// SetReasoningEffort replaces the reasoning-effort level for subsequent turns.
func (a *Agent) SetReasoningEffort(effort ReasoningEffort) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.ReasoningEffort = effort
}

// ReasoningEffort returns the current reasoning-effort level.
func (a *Agent) ReasoningEffort() ReasoningEffort {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.ReasoningEffort
}

// SetTools replaces the tool set available to the agent for subsequent turns.
// The updated list takes effect on the next provider call without losing the
// current conversation history.
func (a *Agent) SetTools(tools []Tool) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Tools = tools
	a.reg = NewToolRegistry(tools)
}

// Tools returns a copy of the agent's current tool set. Use with SetTools to
// append a tool without clobbering the existing ones.
func (a *Agent) Tools() []Tool {
	a.mu.Lock()
	defer a.mu.Unlock()
	out := make([]Tool, len(a.cfg.Tools))
	copy(out, a.cfg.Tools)
	return out
}

// SteeringSource supplies mid-turn steering messages typed by the user while
// the agent is running. It mirrors pi's getSteeringMessages hook. Drain must
// atomically return and remove all currently-pending messages.
type SteeringSource interface {
	Drain() []string
}

// SetSteeringSource wires the queue the agent polls between stream rounds for
// mid-turn steering. Pass nil to disable (tests / single-shot runners).
func (a *Agent) SetSteeringSource(s SteeringSource) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.steeringSource = s
}

// IsProcessing reports whether the agent is currently executing a turn
// (including draining its internal queue between turns). The AgentManager
// uses it to report busy state for externally driven turns — e.g. goal
// continuation turns from GoalDriver, which call agent.Run directly and never
// flip the manager's running flag.
func (a *Agent) IsProcessing() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.processing
}

// drainSteeringIntoHistory appends any pending steering messages to history as
// user messages at the current tail. Called between stream rounds: after the
// previous round's assistant/tool messages are appended and before the next
// runStreamRound, so the very next provider request already contains the
// steering. Because the messages are only ever appended at the tail, request
// N+1 stays a strict prefix-extension of request N (guideline #9).
func (a *Agent) drainSteeringIntoHistory() {
	a.mu.Lock()
	src := a.steeringSource
	a.mu.Unlock()
	if src == nil {
		return
	}
	pending := src.Drain()
	for _, text := range pending {
		text = strings.TrimSpace(text)
		if text == "" {
			continue
		}
		msg := Message{Type: Content, Role: User, Content: text, Metadata: map[string]string{metaSteeringDrained: "true"}}
		a.mu.Lock()
		a.history = append(a.history, msg)
		a.mu.Unlock()
		a.emitMessage(msg)
	}
}

// InjectSystemMessage appends a system message to the conversation history.
// It is sent to the model on the next turn so the model can be informed of
// runtime changes (for example newly enabled tools) without losing history.
func (a *Agent) InjectSystemMessage(content string) {
	msg := Message{Type: Content, Role: System, Content: content}
	a.mu.Lock()
	a.history = append(a.history, msg)
	a.mu.Unlock()
	a.emitMessage(msg)
}

// metaEphemeral marks a history message as transient: it is sent to the model
// during the turn it is injected but stripped before the next turn so it does
// not pollute future context (e.g. the recovery hint or the repeat-loop nudge).
// The tag lives in Message.Metadata, which migrateMessage does not forward, so
// the model never sees the tag itself (only the message content, during its turn).
const metaEphemeral = "ephemeral"

// metaSteeringDrained marks a user message that was woven into the turn from
// the mid-turn steering queue (drainSteeringIntoHistory). The TUI uses it to
// clear the pending steering bubble and render the consumed text in its place,
// since the bubble would otherwise linger after the queue has been drained.
// Like metaEphemeral, the tag lives in Message.Metadata and is never sent to
// the model (migrateMessage drops Metadata).
const metaSteeringDrained = "steering_drained"

// InjectEphemeralSystemMessage appends a system message that is relevant only
// for the current turn. It is sent to the model now but stripped from history
// at turn end so it is not re-sent (and does not add noise/context) on future
// turns. Use for transient nudges (e.g. the recovery hint); use
// InjectSystemMessage for durable runtime notices (tool changes).
//
// The message is also surfaced to the user as a durable chat bubble so every
// nudge sent to the model is visible and part of the chat history
// the user MUST be aware of nudges). Host control notes (prefixed "[goa-system]")
// are emitted as a system-notification content event, which the app renders as
// a persistent bubble (the same path used for "Error: 401" notices).
func (a *Agent) InjectEphemeralSystemMessage(content string) {
	msg := Message{
		Type:     Content,
		Role:     System,
		Content:  content,
		Metadata: map[string]string{metaEphemeral: "true"},
	}
	a.mu.Lock()
	a.history = append(a.history, msg)
	a.mu.Unlock()

	// Surface control nudges so the user can see why the agent changed behavior.
	// The message is ephemeral to the model history, but the notification is
	// intentionally visible in the chat transcript.
	if strings.HasPrefix(content, "[goa-system]") {
		a.emitEvent(OutputEvent{
			Type: EventContent, Role: System, Text: content,
			Metadata: map[string]string{"category": "system-notification"},
		})
	}
}

// stripEphemeralSystemMessages removes ephemeral system messages from history.
// Called at turn end so transient nudges (e.g. the recovery hint) do not persist
// into the next turn's context.
func (a *Agent) stripEphemeralSystemMessages() {
	a.mu.Lock()
	defer a.mu.Unlock()
	if len(a.history) == 0 {
		return
	}
	filtered := a.history[:0]
	for _, m := range a.history {
		if m.Role == System && m.Metadata != nil && m.Metadata[metaEphemeral] == "true" {
			continue
		}
		filtered = append(filtered, m)
	}
	a.history = filtered
}

// Model returns the active model configuration.
func (a *Agent) Model() provider.Model {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.Model
}

// StreamOptions returns the configured stream options.
func (a *Agent) StreamOptions() provider.StreamOptions {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.StreamOptions
}

// SpillPolicy returns the configured tool-result spill policy (nil when the
// policy is disabled).
func (a *Agent) SpillPolicy() SpillPolicy {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.cfg.SpillPolicy
}

// SetStreamOptions replaces the stream options for subsequent turns.
// This updates the API key, headers, timeout, transport, and other provider
// settings. Call after switching providers so the new provider's credentials
// are used on the next turn.
// SetContextWindow updates the model's advertised context window at runtime.
// Used by the host to refresh the loaded context length for local providers
// after the model has finished loading.
func (a *Agent) SetContextWindow(nCtx int) {
	if nCtx <= 0 {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.Model.ContextWindow = nCtx
	a.contextWindow.Store(int64(nCtx))
}

func (a *Agent) SetStreamOptions(opts provider.StreamOptions) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.cfg.StreamOptions = opts
	if opts.APIKey != "" {
		a.cfg.APIKey = opts.APIKey
	}
}

// GetHistory returns a copy of the conversation history.
func (a *Agent) GetHistory() []Message {
	a.mu.Lock()
	defer a.mu.Unlock()

	result := make([]Message, len(a.history))
	copy(result, a.history)
	return result
}

// observerEntry pairs an OutputObserver with a unique ID used as an identity
// handle for removal. The id is what AddObserver returns (as a remove handle);
// observer values themselves may be non-comparable function types.
