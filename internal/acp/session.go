// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package acp

import (
	"context"
	"fmt"
	"sync"

	"github.com/pijalu/goa/internal/agentic"
)

// ACPSession wraps an ACP session with its lifecycle and event forwarding.
type ACPSession struct {
	ID     string
	conn   ServerConn
	driver AgentDriver
	cancel context.CancelFunc
	mu     sync.Mutex
}

// NewACPSession creates a new ACP session.
func NewACPSession(id string, conn ServerConn, driver AgentDriver) *ACPSession {
	return &ACPSession{
		ID:     id,
		conn:   conn,
		driver: driver,
	}
}

// Start initializes the real agent session and starts forwarding events.
func (s *ACPSession) Start() error {
	if s.driver == nil {
		return nil
	}
	if err := s.driver.StartSession(); err != nil {
		return err
	}

	ctx, cancel := context.WithCancel(context.Background())
	s.mu.Lock()
	s.cancel = cancel
	s.mu.Unlock()

	go s.forwardEvents(ctx)
	return nil
}

// ProcessPrompt sends user input to the agent or simulates a response.
func (s *ACPSession) ProcessPrompt(text string) {
	if s.driver == nil {
		s.conn.SendNotification("session/update", SessionUpdate{
			SessionID: s.ID,
			Event: SessionEvent{
				Type:    "agent_message_chunk",
				Content: fmt.Sprintf("Received: %s", truncateText(text, 100)),
			},
		})
		return
	}

	if err := s.driver.SendUserInput(text); err != nil {
		s.conn.SendNotification("session/update", SessionUpdate{
			SessionID: s.ID,
			Event: SessionEvent{
				Type:    "error",
				Content: err.Error(),
			},
		})
	}
}

// Cancel interrupts the current prompt processing.
func (s *ACPSession) Cancel() {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cancel != nil {
		s.cancel()
		s.cancel = nil
	}
	if s.driver != nil {
		s.driver.Interrupt()
	}
}

func (s *ACPSession) forwardEvents(ctx context.Context) {
	if s.driver == nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case ev, ok := <-s.driver.Events():
			if !ok {
				return
			}
			s.sendEvent(ev)
		}
	}
}

func (s *ACPSession) sendEvent(ev agentic.OutputEvent) {
	update := SessionUpdate{SessionID: s.ID}
	switch ev.Type {
	case agentic.EventContent:
		if ev.Role == agentic.User || ev.Role == agentic.System {
			return
		}
		eventType := "agent_message_chunk"
		if ev.State == agentic.StateThinking {
			eventType = "thinking_chunk"
		}
		update.Event = SessionEvent{Type: eventType, Content: ev.Text}
	case agentic.EventToolCall:
		update.Event = SessionEvent{
			Type: "tool_call",
			ToolCall: &ToolCallEvent{
				ToolCallID: ev.ToolCallID,
				Name:       ev.ToolName,
				Arguments:  ev.ToolInput,
			},
		}
	case agentic.EventToolResult:
		update.Event = SessionEvent{
			Type: "tool_result",
			ToolResult: &ToolResultEvent{
				ToolCallID: ev.ToolCallID,
				Content:    ev.Text,
			},
		}
	case agentic.EventEnd:
		update.Event = SessionEvent{Type: "turn_end"}
	default:
		return
	}
	s.conn.SendNotification("session/update", update)
}

func truncateText(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	return string(runes[:n]) + "..."
}
