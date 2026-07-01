// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package acp

import (
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/pijalu/goa/internal/agentic"
)

// fakeConn records notifications sent by an ACPSession.
type fakeConn struct {
	mu            sync.Mutex
	notifications []SessionUpdate
}

func (c *fakeConn) SendResponse(id json.RawMessage, result interface{}) {}
func (c *fakeConn) SendError(id json.RawMessage, err *RPCError)         {}
func (c *fakeConn) SendNotification(method string, params interface{}) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if u, ok := params.(SessionUpdate); ok {
		c.notifications = append(c.notifications, u)
	}
}
func (c *fakeConn) SessionID() string { return "" }

// fakeAgentDriver simulates a real agent for ACP forwarding tests.
type fakeAgentDriver struct {
	mu      sync.Mutex
	events  chan agentic.OutputEvent
	inputs  []string
	started bool
}

func newFakeAgentDriver() *fakeAgentDriver {
	return &fakeAgentDriver{events: make(chan agentic.OutputEvent, 8)}
}

func (d *fakeAgentDriver) StartSession() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.started = true
	return nil
}

func (d *fakeAgentDriver) SendUserInput(input string) error {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.inputs = append(d.inputs, input)
	return nil
}

func (d *fakeAgentDriver) Events() <-chan agentic.OutputEvent {
	return d.events
}

func (d *fakeAgentDriver) Interrupt() {}

func (d *fakeAgentDriver) emit(ev agentic.OutputEvent) {
	d.events <- ev
}

func TestACPSession_ForwardsAgentEvents(t *testing.T) {
	conn := &fakeConn{}
	driver := newFakeAgentDriver()
	session := NewACPSession("acp-test", conn, driver)

	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if !driver.started {
		t.Fatal("driver.StartSession was not called")
	}

	driver.emit(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, Text: "hello"})
	driver.emit(agentic.OutputEvent{Type: agentic.EventToolCall, ToolName: "read", ToolInput: `{"path":"x"}`, ToolCallID: "tc1"})
	driver.emit(agentic.OutputEvent{Type: agentic.EventToolResult, Text: "content", ToolCallID: "tc1"})
	driver.emit(agentic.OutputEvent{Type: agentic.EventEnd})

	waitForNotifications(conn, 4)

	conn.mu.Lock()
	defer conn.mu.Unlock()
	if len(conn.notifications) != 4 {
		t.Fatalf("got %d notifications, want 4", len(conn.notifications))
	}
	if conn.notifications[0].Event.Type != "agent_message_chunk" || conn.notifications[0].Event.Content != "hello" {
		t.Errorf("first event = %+v, want agent_message_chunk hello", conn.notifications[0].Event)
	}
	if conn.notifications[1].Event.Type != "tool_call" || conn.notifications[1].Event.ToolCall.Name != "read" {
		t.Errorf("second event = %+v, want tool_call read", conn.notifications[1].Event)
	}
	if conn.notifications[2].Event.Type != "tool_result" || conn.notifications[2].Event.ToolResult.ToolCallID != "tc1" {
		t.Errorf("third event = %+v, want tool_result tc1", conn.notifications[2].Event)
	}
	if conn.notifications[3].Event.Type != "turn_end" {
		t.Errorf("fourth event = %+v, want turn_end", conn.notifications[3].Event)
	}
}

func TestACPSession_ForwardsThinkingChunk(t *testing.T) {
	conn := &fakeConn{}
	driver := newFakeAgentDriver()
	session := NewACPSession("acp-test", conn, driver)

	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	driver.emit(agentic.OutputEvent{Type: agentic.EventContent, Role: agentic.Assistant, State: agentic.StateThinking, Text: "deep thought"})

	waitForNotifications(conn, 1)

	conn.mu.Lock()
	defer conn.mu.Unlock()
	if len(conn.notifications) != 1 || conn.notifications[0].Event.Type != "thinking_chunk" || conn.notifications[0].Event.Content != "deep thought" {
		t.Errorf("event = %+v, want thinking_chunk deep thought", conn.notifications[0].Event)
	}
}

func TestACPSession_ProcessPromptForwardsInput(t *testing.T) {
	conn := &fakeConn{}
	driver := newFakeAgentDriver()
	session := NewACPSession("acp-test", conn, driver)
	if err := session.Start(); err != nil {
		t.Fatalf("Start: %v", err)
	}

	session.ProcessPrompt("do work")

	driver.mu.Lock()
	defer driver.mu.Unlock()
	if len(driver.inputs) != 1 || driver.inputs[0] != "do work" {
		t.Errorf("inputs = %v, want [do work]", driver.inputs)
	}
}

func TestACPSession_ProcessPromptSimulatesWhenNoDriver(t *testing.T) {
	conn := &fakeConn{}
	session := NewACPSession("acp-test", conn, nil)

	session.ProcessPrompt("hello")

	waitForNotifications(conn, 1)
	conn.mu.Lock()
	defer conn.mu.Unlock()
	if len(conn.notifications) != 1 || conn.notifications[0].Event.Type != "agent_message_chunk" {
		t.Fatalf("expected simulated agent_message_chunk, got %+v", conn.notifications)
	}
}

func waitForNotifications(conn *fakeConn, n int) {
	for i := 0; i < 100; i++ {
		conn.mu.Lock()
		got := len(conn.notifications)
		conn.mu.Unlock()
		if got >= n {
			return
		}
		sleepShort()
	}
}

func sleepShort() { time.Sleep(10 * time.Millisecond) }
