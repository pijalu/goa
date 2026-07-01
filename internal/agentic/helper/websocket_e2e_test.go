// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/pijalu/goa/internal/agentic"
)

// TestWebSocketObserver_E2E tests the full WebSocket streaming flow.
func TestWebSocketObserver_E2E(t *testing.T) {
	// Create hub and observer
	hub := NewWSHub()
	go hub.Run()

	filter := DefaultEventFilter()
	observer := NewWebSocketObserver(hub, filter)

	// Create test server with WebSocket endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := upgrader.Upgrade(w, r, nil)
		if err != nil {
			t.Logf("upgrade error: %v", err)
			return
		}
		defer conn.Close()

		// Read and discard messages asynchronously
		go func() {
			for {
				if _, _, err := conn.ReadMessage(); err != nil {
					return
				}
			}
		}()
		<-make(chan struct{}) // Block until test closes connection
	}))
	defer server.Close()

	// Connect WebSocket client
	url := "ws" + strings.TrimPrefix(server.URL, "http")
	conn, _, err := websocket.DefaultDialer.Dial(url, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()

	// Create client and register
	client := &WSClient{
		hub:  hub,
		send: make(chan []byte, 256),
		conn: conn,
		Metadata: map[string]interface{}{
			"session_id": "test",
		},
	}
	hub.register <- client

	// Wait for registration - client should be added to hub
	time.Sleep(50 * time.Millisecond)

	if hub.ClientCount() != 1 {
		t.Fatalf("expected 1 client, got %d", hub.ClientCount())
	}

	// Simulate agent events (without WebSocket sending)
	ctx := context.Background()
	_ = ctx

	// State change: thinking
	observer.OnEvent(agentic.OutputEvent{
		Type:  agentic.EventStateChange,
		State: agentic.StateThinking,
	})

	// Content: thinking tokens
	observer.OnEvent(agentic.OutputEvent{
		Type:    agentic.EventContent,
		State:   agentic.StateThinking,
		Text:    "Let me think about this...",
		Role:    agentic.Assistant,
		IsDelta: true,
	})

	// State change: content
	observer.OnEvent(agentic.OutputEvent{
		Type:  agentic.EventStateChange,
		State: agentic.StateContent,
	})

	// Content: response
	observer.OnEvent(agentic.OutputEvent{
		Type:    agentic.EventContent,
		State:   agentic.StateContent,
		Text:    "Hello! I'm ready to help.",
		Role:    agentic.Assistant,
		IsDelta: false,
	})

	// Tool call
	observer.OnEvent(agentic.OutputEvent{
		Type:       agentic.EventToolCall,
		ToolName:   "calculator",
		ToolInput:  `{"a":1,"b":2,"op":"add"}`,
		ToolCallID: "call_123",
	})

	// Tool result
	observer.OnEvent(agentic.OutputEvent{
		Type:       agentic.EventContent,
		State:      agentic.StateToolResult,
		Text:       `{"result":3}`,
		Role:       agentic.ToolRole,
		ToolCallID: "call_123",
	})

	// End
	observer.OnEvent(agentic.OutputEvent{
		Type: agentic.EventEnd,
	})

	// Give time for hub to process broadcasts
	time.Sleep(50 * time.Millisecond)

	// Verify messages were sent to the client's send channel
	// (WritePump would normally forward these to WebSocket)
	t.Logf("DEBUG: client.send channel len=%d", len(client.send))

	// Count messages received via client.send
	receivedCount := 0
	for len(client.send) > 0 {
		msg := <-client.send
		receivedCount++
		t.Logf("DEBUG: received message: %s", msg)
	}

	if receivedCount == 0 {
		t.Error("no messages received")
	}

	// We expect at least 7 messages:
	// 1. state: thinking
	// 2. thinking delta
	// 3. state: content
	// 4. content (done)
	// 5. tool_call
	// 6. content (tool result)
	// 7. message (end)
	if receivedCount < 7 {
		t.Errorf("expected at least 7 messages, got %d", receivedCount)
	}

	// We verified messages were received - test passes
	t.Logf("E2E test passed - %d messages delivered to client", receivedCount)
}

// TestWSHub_SessionBroadcast tests session-specific broadcasting.
func TestWSHub_SessionBroadcast(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()

	// Create two clients in different sessions
	client1 := &WSClient{
		hub:  hub,
		send: make(chan []byte, 256),
		Metadata: map[string]interface{}{
			"session_id": "session1",
		},
	}
	client2 := &WSClient{
		hub:  hub,
		send: make(chan []byte, 256),
		Metadata: map[string]interface{}{
			"session_id": "session2",
		},
	}

	hub.register <- client1
	hub.register <- client2
	time.Sleep(50 * time.Millisecond)

	if hub.ClientCount() != 2 {
		t.Fatalf("expected 2 clients, got %d", hub.ClientCount())
	}

	// Broadcast to session1
	hub.BroadcastToSession("session1", []byte(`{"type":"test","for":"session1"}`))

	// Verify client1 received, client2 did not
	select {
	case msg := <-client1.send:
		if !strings.Contains(string(msg), "session1") {
			t.Errorf("client1 got wrong message: %s", msg)
		}
	case <-time.After(time.Second):
		t.Error("client1 timeout")
	}

	// Verify client2 did not receive (non-blocking check)
	select {
	case msg := <-client2.send:
		t.Errorf("client2 should not receive: %s", msg)
	default:
		// Expected - client2 should not receive
	}
}
