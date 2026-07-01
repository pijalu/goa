// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

func TestWebSocketObserver_OnEvent_StateChange(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	obs := NewWebSocketObserver(hub, DefaultEventFilter())

	// Create a mock client
	client := &WSClient{
		hub:  hub,
		send: make(chan []byte, 256),
	}
	hub.register <- client
	time.Sleep(10 * time.Millisecond) // Wait for registration

	// Send state change event
	obs.OnEvent(agentic.OutputEvent{
		Type:  agentic.EventStateChange,
		State: agentic.StateThinking,
	})

	// Check message was broadcast
	select {
	case msg := <-client.send:
		var wsMsg WsMessage
		if err := decodeMessage(msg, &wsMsg); err != nil {
			t.Fatalf("failed to decode message: %v", err)
		}
		if wsMsg.Type != "state" {
			t.Errorf("expected type 'state', got '%s'", wsMsg.Type)
		}
		if wsMsg.State != "thinking" {
			t.Errorf("expected state 'thinking', got '%s'", wsMsg.State)
		}
	case <-hub.broadcast:
		t.Error("no message received")
	}
}

func TestWebSocketObserver_OnEvent_Content(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	filter := DefaultEventFilter()
	filter.StreamContent = true
	obs := NewWebSocketObserver(hub, filter)

	client := &WSClient{
		hub:  hub,
		send: make(chan []byte, 256),
	}
	hub.register <- client
	time.Sleep(10 * time.Millisecond)

	// Send content event
	obs.OnEvent(agentic.OutputEvent{
		Type:  agentic.EventContent,
		State: agentic.StateContent,
		Text:  "Hello, world!",
	})

	select {
	case msg := <-client.send:
		var wsMsg WsMessage
		if err := decodeMessage(msg, &wsMsg); err != nil {
			t.Fatalf("failed to decode message: %v", err)
		}
		if wsMsg.Type != "content" {
			t.Errorf("expected type 'content', got '%s'", wsMsg.Type)
		}
		if wsMsg.Delta != "Hello, world!" {
			t.Errorf("expected delta 'Hello, world!', got '%s'", wsMsg.Delta)
		}
	case <-hub.broadcast:
		t.Error("no message received")
	}
}

func TestWebSocketObserver_OnEvent_ToolCall(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	filter := DefaultEventFilter()
	filter.StreamToolCall = true
	obs := NewWebSocketObserver(hub, filter)

	client := &WSClient{
		hub:  hub,
		send: make(chan []byte, 256),
	}
	hub.register <- client
	time.Sleep(10 * time.Millisecond)

	// Send tool call event
	obs.OnEvent(agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		ToolName:   "calculator",
		ToolInput:  `{"a":1,"b":2,"op":"add"}`,
		ToolCallID: "call_123",
	})

	select {
	case msg := <-client.send:
		var wsMsg WsMessage
		if err := decodeMessage(msg, &wsMsg); err != nil {
			t.Fatalf("failed to decode message: %v", err)
		}
		if wsMsg.Type != "tool_call" {
			t.Errorf("expected type 'tool_call', got '%s'", wsMsg.Type)
		}
		if wsMsg.Name != "calculator" {
			t.Errorf("expected name 'calculator', got '%s'", wsMsg.Name)
		}
		if wsMsg.Input != `{"a":1,"b":2,"op":"add"}` {
			t.Errorf("expected input, got '%s'", wsMsg.Input)
		}
	case <-hub.broadcast:
		t.Error("no message received")
	}
}

func TestWebSocketObserver_FilteredContent(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()
	filter := DefaultEventFilter()
	filter.StreamContent = false // Disabled
	obs := NewWebSocketObserver(hub, filter)

	client := &WSClient{
		hub:  hub,
		send: make(chan []byte, 256),
	}
	hub.register <- client
	time.Sleep(10 * time.Millisecond)

	// Send content event - should be filtered
	obs.OnEvent(agentic.OutputEvent{
		Type:  agentic.EventContent,
		State: agentic.StateContent,
		Text:  "Hello",
	})

	// No message should be sent
	select {
	case <-client.send:
		t.Error("message should have been filtered")
	case <-hub.broadcast:
		t.Error("no broadcast expected for filtered event")
	default:
		// Expected - message was filtered
	}
}

// Helper to decode messages (handles both single and multi-line)
func decodeMessage(data []byte, msg *WsMessage) error {
	// Try single message first
	if err := json.Unmarshal(data, msg); err != nil {
		// Try first line only (multi-message format)
		for i, b := range data {
			if b == '\n' {
				return json.Unmarshal(data[:i], msg)
			}
		}
	}
	return nil
}
