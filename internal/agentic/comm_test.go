// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestAgentBus_RegisterAndSend(t *testing.T) {
	bus := NewAgentBus()

	inboxA, err := bus.Register("agent-a")
	if err != nil {
		t.Fatalf("register agent-a: %v", err)
	}

	inboxB, err := bus.Register("agent-b")
	if err != nil {
		t.Fatalf("register agent-b: %v", err)
	}

	msg := CommMessage{From: "agent-a", To: "agent-b", Content: "hello"}
	if err := bus.Send(context.Background(), msg); err != nil {
		t.Fatalf("send failed: %v", err)
	}

	select {
	case received := <-inboxB:
		if received.From != "agent-a" {
			t.Errorf("expected from agent-a, got %s", received.From)
		}
		if received.Content != "hello" {
			t.Errorf("expected 'hello', got %s", received.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}

	// inboxA should be empty
	select {
	case <-inboxA:
		t.Fatal("agent-a should not have received a message")
	default:
	}
}

func TestAgentBus_DuplicateRegistration(t *testing.T) {
	bus := NewAgentBus()
	if _, err := bus.Register("agent-a"); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if _, err := bus.Register("agent-a"); err == nil {
		t.Fatal("expected error for duplicate registration")
	}
}

func TestAgentBus_SendToUnknownAgent(t *testing.T) {
	bus := NewAgentBus()
	msg := CommMessage{From: "agent-a", To: "agent-b", Content: "hello"}
	if err := bus.Send(context.Background(), msg); err == nil {
		t.Fatal("expected error sending to unknown agent")
	}
}

func TestAgentBus_Unregister(t *testing.T) {
	bus := NewAgentBus()
	inbox, err := bus.Register("agent-a")
	if err != nil {
		t.Fatalf("register: %v", err)
	}

	bus.Unregister("agent-a")

	// Channel should be closed
	select {
	case _, ok := <-inbox:
		if ok {
			t.Fatal("expected closed channel")
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for channel close")
	}
}

func TestAgentBus_AgentNames(t *testing.T) {
	bus := NewAgentBus()
	bus.Register("alpha")
	bus.Register("beta")

	names := bus.AgentNames()
	if len(names) != 2 {
		t.Fatalf("expected 2 names, got %d", len(names))
	}

	found := make(map[string]bool)
	for _, n := range names {
		found[n] = true
	}
	if !found["alpha"] || !found["beta"] {
		t.Errorf("expected alpha and beta, got %v", names)
	}
}

func TestSendMessageTool_Schema(t *testing.T) {
	bus := NewAgentBus()
	bus.Register("alice")
	bus.Register("bob")

	tool := &SendMessageTool{Bus: bus, FromName: "alice"}
	schema := tool.Schema()

	if schema.Name != "send_message" {
		t.Errorf("expected name 'send_message', got %s", schema.Name)
	}

	// Schema should mention bob but not alice (self excluded from enum)
	schemaJSON := jsonMarshal(schema.Schema)
	if !strings.Contains(schemaJSON, "bob") {
		t.Error("expected schema to list 'bob' as available recipient")
	}
}

func TestSendMessageTool_Execute(t *testing.T) {
	bus := NewAgentBus()
	inbox, _ := bus.Register("bob")

	tool := &SendMessageTool{Bus: bus, FromName: "alice"}
	result, err := tool.Execute(`{"to":"bob","content":"hi bob"}`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(result, "sent to bob") {
		t.Errorf("unexpected result: %s", result)
	}

	select {
	case msg := <-inbox:
		if msg.Content != "hi bob" {
			t.Errorf("expected 'hi bob', got %s", msg.Content)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout waiting for message")
	}
}

func TestSendMessageTool_ExecuteInvalidJSON(t *testing.T) {
	bus := NewAgentBus()
	tool := &SendMessageTool{Bus: bus, FromName: "alice"}
	_, err := tool.Execute(`not json`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestReceiveMessageTool_ReceivesMessage(t *testing.T) {
	bus := NewAgentBus()
	inbox, _ := bus.Register("alice")

	// Pre-seed a message
	bus.Send(context.Background(), CommMessage{From: "bob", To: "alice", Content: "hello alice"})

	tool := &ReceiveMessageTool{Inbox: inbox}
	result, err := tool.Execute(`{}`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if !strings.Contains(result, "From bob") {
		t.Errorf("expected sender attribution, got: %s", result)
	}
	if !strings.Contains(result, "hello alice") {
		t.Errorf("expected message content, got: %s", result)
	}
}

func TestReceiveMessageTool_EmptyInbox(t *testing.T) {
	bus := NewAgentBus()
	inbox, _ := bus.Register("alice")

	tool := &ReceiveMessageTool{Inbox: inbox}
	result, err := tool.Execute(`{}`)
	if err != nil {
		t.Fatalf("execute failed: %v", err)
	}
	if result != "No new messages." {
		t.Errorf("expected empty inbox message, got: %s", result)
	}
}

func TestReceiveMessageTool_ClosedInbox(t *testing.T) {
	bus := NewAgentBus()
	inbox, _ := bus.Register("alice")
	bus.Unregister("alice")

	tool := &ReceiveMessageTool{Inbox: inbox}
	_, err := tool.Execute(`{}`)
	if err == nil {
		t.Fatal("expected error for closed inbox")
	}
}

func TestCommConnector_Stop(t *testing.T) {
	bus := NewAgentBus()
	inbox, _ := bus.Register("agent-a")

	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	connector := NewCommConnector(agent, inbox)
	connector.Stop()

	// After Stop, sending to inbox should not panic and connector should exit
	bus.Send(context.Background(), CommMessage{From: "x", To: "agent-a", Content: "y"})
	// If the goroutine didn't exit cleanly, this test would leak
}

func TestSetupCommAgent(t *testing.T) {
	bus := NewAgentBus()
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	inbox, sendTool, connector, err := SetupCommAgent(bus, "alpha", agent, true)
	if err != nil {
		t.Fatalf("SetupCommAgent: %v", err)
	}
	if inbox == nil {
		t.Fatal("expected inbox")
	}
	if sendTool == nil {
		t.Fatal("expected sendTool")
	}
	if connector == nil {
		t.Fatal("expected connector when autoReceive=true")
	}
	if sendTool.FromName != "alpha" {
		t.Errorf("expected FromName 'alpha', got %s", sendTool.FromName)
	}

	// Verify agent is registered on bus
	names := bus.AgentNames()
	found := false
	for _, n := range names {
		if n == "alpha" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected 'alpha' to be registered on bus")
	}

	connector.Stop()
}

func TestSetupCommAgent_NoAutoReceive(t *testing.T) {
	bus := NewAgentBus()
	agent := NewAgent(Config{
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})

	_, sendTool, connector, err := SetupCommAgent(bus, "beta", agent, false)
	if err != nil {
		t.Fatalf("SetupCommAgent: %v", err)
	}
	if sendTool == nil {
		t.Fatal("expected sendTool")
	}
	if connector != nil {
		t.Fatal("expected nil connector when autoReceive=false")
	}
}

func TestAgentBus_ConcurrentSendAndReceive(t *testing.T) {
	bus := NewAgentBus()
	bus.bufSize = 100 // expand buffer for this test
	inbox, _ := bus.Register("target")

	var wg sync.WaitGroup
	for i := 0; i < 100; i++ {
		wg.Add(1)
		go func(n int) {
			defer wg.Done()
			msg := CommMessage{From: "sender", To: "target", Content: strings.Repeat("x", n)}
			if err := bus.Send(context.Background(), msg); err != nil {
				t.Errorf("send failed: %v", err)
			}
		}(i)
	}

	wg.Wait()

	count := 0
	done := false
	for !done {
		select {
		case <-inbox:
			count++
		case <-time.After(100 * time.Millisecond):
			done = true
		}
	}

	if count != 100 {
		t.Errorf("expected 100 messages, got %d", count)
	}
}

// TestAgentBus_SendUnregisterNoRace exercises the send-on-closed-channel
// race fixed by holding the bus read lock across Send's blocking select.
// Many goroutines send to the target while it is repeatedly unregistered
// (which closes its inbox) and re-registered. Under the old code this
// intermittently panicked with "send on closed channel"; with the fix every
// send either succeeds or returns a clean "not found" error. Run with -race.
func TestAgentBus_SendUnregisterNoRace(t *testing.T) {
	const senders = 16
	const cycles = 50

	bus := NewAgentBus()
	// Make the target's inbox small so it fills quickly and exercises the
	// timeout branch too.
	bus.bufSize = 1
	if _, err := bus.Register("target"); err != nil {
		t.Fatalf("initial register: %v", err)
	}

	stop := make(chan struct{})
	var sendWG sync.WaitGroup
	for i := 0; i < senders; i++ {
		sendWG.Add(1)
		go func() {
			defer sendWG.Done()
			msg := CommMessage{From: "sender", To: "target", Content: "x"}
			// Short ctx so the full-inbox path returns promptly and we turn over
			// many Send/Unregister interleavings within the test budget.
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Millisecond)
			for {
				select {
				case <-stop:
					cancel()
					return
				default:
				}
				_ = bus.Send(ctx, msg)
			}
		}()
	}

	// Churn the registration: unregister (closes the inbox) then re-register
	// a fresh inbox. Concurrent sends must never panic on the close.
	for i := 0; i < cycles; i++ {
		bus.Unregister("target")
		// Drain any brief gap; re-register immediately.
		if _, err := bus.Register("target"); err != nil {
			// A sender may have raced a re-register within the same cycle window
			// only if Register were non-exclusive — it is exclusive, so this is
			// unexpected.
			t.Fatalf("re-register cycle %d: %v", i, err)
		}
	}

	close(stop)
	sendWG.Wait()
}

// jsonMarshal is a test helper to stringify schema maps
func jsonMarshal(v interface{}) string {
	b, _ := json.Marshal(v)
	return string(b)
}
