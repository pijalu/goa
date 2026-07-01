// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

// EventFilter configures which event types to stream to WebSocket clients.
type EventFilter struct {
	StreamThinking bool // Stream reasoning tokens
	StreamContent  bool // Stream content delta
	StreamToolCall bool // Stream tool call requests
	StreamMessage  bool // Stream complete messages
}

// DefaultEventFilter returns a filter that streams all event types.
func DefaultEventFilter() EventFilter {
	return EventFilter{
		StreamThinking: true,
		StreamContent:  true,
		StreamToolCall: true,
		StreamMessage:  true,
	}
}

// WsMessage represents a message sent to WebSocket clients.
type WsMessage struct {
	Type    string             `json:"type"`              // thinking|content|tool_call|message|state
	Done    bool               `json:"done,omitempty"`    // Final message flag
	Name    string             `json:"name,omitempty"`    // Tool name (tool_call)
	Input   string             `json:"input,omitempty"`   // Tool input JSON (tool_call)
	Message *StructuredMessage `json:"message,omitempty"` // Complete message (message)
	State   string             `json:"state,omitempty"`   // Current state (state)
	Delta   string             `json:"delta,omitempty"`   // Text delta (streaming)
}

// NewWsMessage creates a new WsMessage with the specified type.
func NewWsMessage(msgType string) WsMessage {
	return WsMessage{Type: msgType}
}
