// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"encoding/json"

	"github.com/pijalu/goa/internal/agentic"
)

// WebSocketObserver receives output events and broadcasts them to WebSocket clients.
type WebSocketObserver struct {
	hub    *WSHub
	filter EventFilter
}

// NewWebSocketObserver creates a new WebSocketObserver with the given hub and filter.
func NewWebSocketObserver(hub *WSHub, filter EventFilter) *WebSocketObserver {
	return &WebSocketObserver{
		hub:    hub,
		filter: filter,
	}
}

// OnEvent implements agentic.OutputObserver.
func (w *WebSocketObserver) OnEvent(event agentic.OutputEvent) {
	switch event.Type {
	case agentic.EventStateChange:
		w.handleStateChange(event)
	case agentic.EventContent:
		w.handleContent(event)
	case agentic.EventToolCall:
		w.handleToolCall(event)
	case agentic.EventToolResult:
		w.handleToolResult(event)
	case agentic.EventEnd:
		w.handleEnd(event)
	}
}

func (w *WebSocketObserver) handleStateChange(event agentic.OutputEvent) {
	msg := WsMessage{
		Type:  "state",
		State: event.State.String(),
	}
	w.broadcast(msg)
}

func (w *WebSocketObserver) handleContent(event agentic.OutputEvent) {
	// Check filter
	if event.State == agentic.StateThinking && !w.filter.StreamThinking {
		return
	}
	if event.State == agentic.StateContent && !w.filter.StreamContent {
		return
	}

	msgType := "content"
	if event.State == agentic.StateThinking {
		msgType = "thinking"
	}

	msg := WsMessage{
		Type:  msgType,
		Delta: event.Text,
		Done:  !event.IsDelta,
	}
	w.broadcast(msg)
}

func (w *WebSocketObserver) handleToolCall(event agentic.OutputEvent) {
	if !w.filter.StreamToolCall {
		return
	}

	msg := WsMessage{
		Type:  "tool_call",
		Name:  event.ToolName,
		Input: event.ToolInput,
		Done:  true,
	}
	w.broadcast(msg)
}

func (w *WebSocketObserver) handleToolResult(event agentic.OutputEvent) {
	// Tool results are included in the content stream
	msg := WsMessage{
		Type:  "content",
		Delta: event.Text,
		Done:  true,
	}
	w.broadcast(msg)
}

func (w *WebSocketObserver) handleEnd(event agentic.OutputEvent) {
	msg := WsMessage{
		Type: "message",
		Done: true,
	}
	w.broadcast(msg)
}

func (w *WebSocketObserver) broadcast(wsMsg WsMessage) {
	data, err := json.Marshal(wsMsg)
	if err != nil {
		return
	}
	w.hub.Broadcast(data)
}
