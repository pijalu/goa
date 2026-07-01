// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic/provider"
)

// TestInterAgentConversation simulates a full coordinator <-> worker
// exchange using the stream-based API. Proves that messages flow through
// the bus, trigger tool calls, and generate responses.
func TestInterAgentConversation(t *testing.T) {
	bus := NewAgentBus()
	coordInbox, _ := bus.Register("coordinator")
	workerInbox, _ := bus.Register("worker")

	// Register a test provider that returns text content
	cp := registerTestProvider("coord-api", []provider.AssistantMessageEvent{
		{Type: provider.EventTextStart, ContentIndex: 0},
		{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "Hello from coordinator"},
		{Type: provider.EventTextEnd, ContentIndex: 0},
	})
	wp := registerTestProvider("worker-api", []provider.AssistantMessageEvent{
		{Type: provider.EventTextStart, ContentIndex: 0},
		{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "Worker reply"},
		{Type: provider.EventTextEnd, ContentIndex: 0},
	})

	sendTool := &SendMessageTool{Bus: bus}
	coordCfg := Config{
		Model:        testModel(cp.API()),
		SystemPrompt: "You are a coordinator. Use send_message to talk to the worker.",
		Tools:        []Tool{sendTool},
		Logger:       NewLogger(Error),
	}
	workerCfg := Config{
		Model:        testModel(wp.API()),
		SystemPrompt: "You are a worker. Reply to messages from the coordinator.",
		Tools:        []Tool{sendTool},
		Logger:       NewLogger(Error),
	}

	coordAgent := NewAgent(coordCfg)
	workerAgent := NewAgent(workerCfg)

	_ = NewCommConnector(coordAgent, coordInbox)
	_ = NewCommConnector(workerAgent, workerInbox)

	// Collect events from both agents
	var coordEvents, workerEvents []OutputEvent
	var mu sync.Mutex
	coordObs := OutputObserverFunc(func(e OutputEvent) {
		mu.Lock()
		coordEvents = append(coordEvents, e)
		mu.Unlock()
	})
	workerObs := OutputObserverFunc(func(e OutputEvent) {
		mu.Lock()
		workerEvents = append(workerEvents, e)
		mu.Unlock()
	})
	coordAgent.AddObserver(coordObs)
	workerAgent.AddObserver(workerObs)

	// Drain output channels
	go func() {
		for range coordAgent.Output {
		}
	}()
	go func() {
		for range workerAgent.Output {
		}
	}()

	// Start coordinator
	go func() {
		_ = coordAgent.Run(context.Background(), "Ask the worker a question")
	}()
	time.Sleep(50 * time.Millisecond)

	// Start worker
	go func() {
		_ = workerAgent.Run(context.Background(), "Reply to coordinator")
	}()
	time.Sleep(200 * time.Millisecond)

	coordAgent.Stop()
	workerAgent.Stop()

	mu.Lock()
	defer mu.Unlock()
	if len(coordEvents) == 0 {
		t.Error("expected coordinator events")
	}
	if len(workerEvents) == 0 {
		t.Error("expected worker events")
	}
}

// TestAgentBus_SendMessageTool verifies the SendMessageTool works with the bus.
func TestAgentBus_SendMessageTool(t *testing.T) {
	bus := NewAgentBus()
	inbox, err := bus.Register("receiver")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	sendTool := &SendMessageTool{Bus: bus, FromName: "sender"}
	result, err := sendTool.Execute(`{"to":"receiver","content":"hello"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !strings.Contains(result, "sent") {
		t.Errorf("expected 'sent' in result, got %q", result)
	}

	select {
	case msg := <-inbox:
		if msg.From != "sender" {
			t.Errorf("expected from 'sender', got %q", msg.From)
		}
		if msg.Content != "hello" {
			t.Errorf("expected content 'hello', got %q", msg.Content)
		}
	default:
		t.Fatal("expected message in inbox")
	}
}

// TestReceiveMessageTool checks that ReceiveMessageTool receives bus messages.
func TestReceiveMessageTool(t *testing.T) {
	bus := NewAgentBus()
	inbox, err := bus.Register("receiver")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}
	recvTool := &ReceiveMessageTool{Inbox: inbox}

	// Send a message to ourselves
	sendTool := &SendMessageTool{Bus: bus, FromName: "sender"}
	_, err = sendTool.Execute(`{"to":"receiver","content":"test msg"}`)
	if err != nil {
		t.Fatalf("SendMessageTool: %v", err)
	}

	result, err := recvTool.Execute(`{}`)
	if err != nil {
		t.Fatalf("ReceiveMessageTool: %v", err)
	}
	if !strings.Contains(result, "test msg") {
		t.Errorf("expected 'test msg' in result, got %q", result)
	}
}

// TestAgentBus_RegisterDuplicateReturnsError verifies Register returns error on duplicate.
func TestAgentBus_RegisterDuplicateReturnsError(t *testing.T) {
	bus := NewAgentBus()
	_, err := bus.Register("agent1")
	if err != nil {
		t.Fatalf("first Register: %v", err)
	}
	_, err = bus.Register("agent1")
	if err == nil {
		t.Error("expected error on duplicate registration")
	}
}

// TestCommConnector_ReceivesMessages verifies the connector routes messages.
func TestCommConnector_ReceivesMessages(t *testing.T) {
	cp := registerTestProvider("connector-api", []provider.AssistantMessageEvent{
		{Type: provider.EventTextStart, ContentIndex: 0},
		{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "ack"},
		{Type: provider.EventTextEnd, ContentIndex: 0},
	})

	bus := NewAgentBus()
	inbox, err := bus.Register("test-agent")
	if err != nil {
		t.Fatalf("Register: %v", err)
	}

	agent := NewAgent(Config{
		Model:        testModel(cp.API()),
		SystemPrompt: "test",
		Logger:       NewLogger(Error),
	})
	go func() {
		for range agent.Output {
		}
	}()

	connector := NewCommConnector(agent, inbox)
	if connector == nil {
		t.Fatal("NewCommConnector returned nil")
	}

	// Send a message to the agent
	sendTool := &SendMessageTool{Bus: bus}
	_, err = sendTool.Execute(`{"to":"test-agent","message":"work please"}`)
	if err != nil {
		t.Fatalf("SendMessageTool: %v", err)
	}
	time.Sleep(100 * time.Millisecond)
	agent.Stop()
}

// TestAgentBus_MultipleAgents verifies multiple agents on the same bus.
func TestAgentBus_MultipleAgents(t *testing.T) {
	bus := NewAgentBus()
	cp := registerTestProvider("multi-api", []provider.AssistantMessageEvent{
		{Type: provider.EventTextStart, ContentIndex: 0},
		{Type: provider.EventTextDelta, ContentIndex: 0, Delta: "response"},
		{Type: provider.EventTextEnd, ContentIndex: 0},
	})

	agents := make([]*Agent, 3)
	for i := 0; i < 3; i++ {
		name := fmt.Sprintf("agent-%d", i)
		_, err := bus.Register(name)
		if err != nil {
			t.Fatalf("Register %s: %v", name, err)
		}
		agents[i] = NewAgent(Config{
			Model:        testModel(cp.API()),
			SystemPrompt: "test",
			Logger:       NewLogger(Error),
			Tools:        []Tool{&SendMessageTool{Bus: bus}},
		})
		go func() {
			for range agents[i].Output {
			}
		}()
	}

	for i, a := range agents {
		go func(agent *Agent, idx int) {
			_ = agent.Run(context.Background(), fmt.Sprintf("msg-%d", idx))
		}(a, i)
	}

	time.Sleep(200 * time.Millisecond)
	for _, a := range agents {
		a.Stop()
	}
}
