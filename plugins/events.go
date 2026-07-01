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

// Emit dispatches an event to all registered handlers.
func (b *EventBus) Emit(eventName string, payload interface{}) {
	if handlers, ok := b.handlers[eventName]; ok {
		for _, h := range handlers {
			h(eventName, payload)
		}
	}
}
