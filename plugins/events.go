// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

// Event type constants for plugin event bus.
const (
	EventModeChanged   = "mode.changed"
	EventSkillChanged  = "skill.changed"
	EventToolCall      = "tool.call"
	EventToolResult    = "tool.result"
	EventPipelineStage = "pipeline.stage"
	EventSessionStart  = "session.start"
	EventSessionEnd    = "session.end"
)

// EventHandler is a function that handles an event payload.
type EventHandler func(eventName string, payload interface{})

// EventBus provides a JS API for plugins to listen to events.
type EventBus struct {
	handlers map[string][]EventHandler
}

// NewEventBus creates a new event bus.
func NewEventBus() *EventBus {
	return &EventBus{
		handlers: make(map[string][]EventHandler),
	}
}

// On registers a handler for the given event type.
func (b *EventBus) On(eventName string, handler EventHandler) {
	b.handlers[eventName] = append(b.handlers[eventName], handler)
}

// Emit dispatches an event to handlers registered for the specific event
// name plus any wildcard ("*") handlers, which observe every event.
func (b *EventBus) Emit(eventName string, payload interface{}) {
	for _, h := range b.handlers[eventName] {
		h(eventName, payload)
	}
	for _, h := range b.handlers["*"] {
		h(eventName, payload)
	}
}
