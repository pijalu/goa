// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"testing"
	"time"
)

func TestWSHub_RegisterUnregister(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()

	client := &WSClient{
		hub:      hub,
		send:     make(chan []byte, 256),
		Metadata: map[string]interface{}{"session_id": "test"},
	}

	// Register client
	hub.register <- client

	// Wait for goroutine to process
	time.Sleep(10 * time.Millisecond)

	// Check client is tracked
	hub.mu.RLock()
	_, ok := hub.clients[client]
	hub.mu.RUnlock()
	if !ok {
		t.Error("client not registered")
	}

	// Unregister client
	hub.unregister <- client

	// Wait for goroutine to process
	time.Sleep(10 * time.Millisecond)

	// Check client is removed
	hub.mu.RLock()
	_, ok = hub.clients[client]
	hub.mu.RUnlock()
	if ok {
		t.Error("client not unregistered")
	}
}

func TestWSHub_Broadcast(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()

	time.Sleep(10 * time.Millisecond)

	// Create two clients
	client1 := &WSClient{
		hub:      hub,
		send:     make(chan []byte, 256),
		Metadata: map[string]interface{}{"session_id": "test"},
	}
	client2 := &WSClient{
		hub:      hub,
		send:     make(chan []byte, 256),
		Metadata: map[string]interface{}{"session_id": "test"},
	}

	hub.register <- client1
	hub.register <- client2

	// Give time for registration
	time.Sleep(20 * time.Millisecond)

	// Broadcast message
	testMsg := []byte(`{"type":"test"}`)
	hub.Broadcast(testMsg)

	// Both clients should receive
	select {
	case msg := <-client1.send:
		if string(msg) != string(testMsg) {
			t.Errorf("client1 got wrong message: got %s, want %s", msg, testMsg)
		}
	case <-client1.send:
		// channel closed
	}

	select {
	case msg := <-client2.send:
		if string(msg) != string(testMsg) {
			t.Errorf("client2 got wrong message: got %s, want %s", msg, testMsg)
		}
	case <-client2.send:
		// channel closed
	}
}

func TestWSHub_BroadcastToSession(t *testing.T) {
	hub := NewWSHub()
	go hub.Run()

	time.Sleep(10 * time.Millisecond)

	// Create clients with different sessions
	client1 := &WSClient{
		hub:      hub,
		send:     make(chan []byte, 256),
		Metadata: map[string]interface{}{"session_id": "session1"},
	}
	client2 := &WSClient{
		hub:      hub,
		send:     make(chan []byte, 256),
		Metadata: map[string]interface{}{"session_id": "session2"},
	}

	hub.register <- client1
	hub.register <- client2
	time.Sleep(20 * time.Millisecond)

	// Broadcast to session1 only
	testMsg := []byte(`{"type":"test"}`)
	hub.BroadcastToSession("session1", testMsg)

	// client1 should receive, client2 should NOT receive anything
	select {
	case msg := <-client1.send:
		if string(msg) != string(testMsg) {
			t.Errorf("client1 got wrong message: got %s, want %s", msg, testMsg)
		}
	case <-time.After(100 * time.Millisecond):
		t.Error("client1 should have received message")
	}

	// Verify client2 didn't receive anything (non-blocking check)
	select {
	case <-client2.send:
		t.Error("client2 should not receive session-specific message")
	default:
		// This is expected - client2 should not have received
	}
}
