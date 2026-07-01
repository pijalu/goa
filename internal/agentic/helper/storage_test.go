// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"os"
	"testing"

	"github.com/pijalu/goa/internal/agentic"
)

func TestMessageStorageObserver_OnEvent(t *testing.T) {
	// Create temp database
	tmpFile, err := os.CreateTemp("", "storage_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	obs, err := NewMessageStorageObserver(tmpFile.Name(), WithBatchSize(2))
	if err != nil {
		t.Fatal(err)
	}
	defer obs.Close()

	// Send events to build a message
	obs.OnEvent(agentic.OutputEvent{
		Type:  agentic.EventStateChange,
		State: agentic.StateContent,
	})
	obs.OnEvent(agentic.OutputEvent{
		Type:  agentic.EventContent,
		State: agentic.StateContent,
		Text:  "Hello",
		Role:  agentic.Assistant,
	})
	obs.OnEvent(agentic.OutputEvent{
		Type: agentic.EventEnd,
	})

	// Force flush
	obs.Flush()

	// Query messages
	messages, err := obs.GetMessagesBySession("default")
	if err != nil {
		t.Fatal(err)
	}

	if len(messages) != 1 {
		t.Errorf("expected 1 message, got %d", len(messages))
	}

	if len(messages[0].Elements) == 0 {
		t.Error("message should have elements")
	}
}

func TestMessageStorageObserver_BatchFlush(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "storage_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	// Create observer with batch size of 2
	obs, err := NewMessageStorageObserver(tmpFile.Name(), WithBatchSize(2))
	if err != nil {
		t.Fatal(err)
	}
	defer obs.Close()

	// Send first message
	obs.OnEvent(agentic.OutputEvent{
		Type:  agentic.EventStateChange,
		State: agentic.StateContent,
	})
	obs.OnEvent(agentic.OutputEvent{
		Type: agentic.EventContent,
		Text: "Message 1",
		Role: agentic.Assistant,
	})
	obs.OnEvent(agentic.OutputEvent{
		Type: agentic.EventEnd,
	})

	// At this point, batch size not reached, message should be in buffer
	// Send second message to trigger flush
	obs.OnEvent(agentic.OutputEvent{
		Type:  agentic.EventStateChange,
		State: agentic.StateContent,
	})
	obs.OnEvent(agentic.OutputEvent{
		Type: agentic.EventContent,
		Text: "Message 2",
		Role: agentic.Assistant,
	})
	obs.OnEvent(agentic.OutputEvent{
		Type: agentic.EventEnd,
	})

	// Both messages should be flushed
	messages, err := obs.GetMessagesBySession("default")
	if err != nil {
		t.Fatal(err)
	}

	if len(messages) != 2 {
		t.Errorf("expected 2 messages, got %d", len(messages))
	}
}

func TestMessageStorageObserver_Sessions(t *testing.T) {
	tmpFile, err := os.CreateTemp("", "storage_test_*.db")
	if err != nil {
		t.Fatal(err)
	}
	defer os.Remove(tmpFile.Name())
	tmpFile.Close()

	obs, err := NewMessageStorageObserver(tmpFile.Name())
	if err != nil {
		t.Fatal(err)
	}
	defer obs.Close()

	// Create messages in different sessions
	obs.SetSessionID("session1")
	obs.OnEvent(agentic.OutputEvent{
		Type:  agentic.EventStateChange,
		State: agentic.StateContent,
	})
	obs.OnEvent(agentic.OutputEvent{
		Type: agentic.EventContent,
		Text: "Hello from session1",
		Role: agentic.Assistant,
	})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventEnd})

	obs.SetSessionID("session2")
	obs.OnEvent(agentic.OutputEvent{
		Type:  agentic.EventStateChange,
		State: agentic.StateContent,
	})
	obs.OnEvent(agentic.OutputEvent{
		Type: agentic.EventContent,
		Text: "Hello from session2",
		Role: agentic.Assistant,
	})
	obs.OnEvent(agentic.OutputEvent{Type: agentic.EventEnd})

	obs.Flush()

	// Query each session
	messages1, err := obs.GetMessagesBySession("session1")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages1) != 1 {
		t.Errorf("session1: expected 1 message, got %d", len(messages1))
	}

	messages2, err := obs.GetMessagesBySession("session2")
	if err != nil {
		t.Fatal(err)
	}
	if len(messages2) != 1 {
		t.Errorf("session2: expected 1 message, got %d", len(messages2))
	}
}
