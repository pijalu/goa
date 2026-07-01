// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package helper

import (
	"encoding/json"

	"github.com/pijalu/goa/internal/agentic"
)

// StructuredMessage represents a message in the conversation history with composable elements.
type StructuredMessage struct {
	Role           string                  `json:"role"`
	Elements       []MessageElement        `json:"elements"`
	Timings        *agentic.TokenTimings   `json:"timings,omitempty"`
	PromptProgress *agentic.PromptProgress `json:"prompt_progress,omitempty"`
}

// MessageElement represents a single composable part of a message.
type MessageElement struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolInput  string `json:"tool_input,omitempty"`
	ToolResult string `json:"tool_result,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
}

// MessageLogObserver builds a structured conversation history from output events.
type MessageLogObserver struct {
	history          []StructuredMessage
	current          *StructuredMessage
	pendingToolCalls map[string]*MessageElement
	state            agentic.OutputState
}

// NewMessageLogObserver creates a new MessageLogObserver.
func NewMessageLogObserver() *MessageLogObserver {
	return &MessageLogObserver{
		pendingToolCalls: make(map[string]*MessageElement),
	}
}

// OnEvent implements agentic.OutputObserver.
func (m *MessageLogObserver) OnEvent(event agentic.OutputEvent) {
	switch event.Type {
	case agentic.EventStateChange:
		m.handleStateChange(event)
	case agentic.EventContent:
		m.handleContent(event)
	case agentic.EventToolCall:
		m.handleToolCall(event)
	case agentic.EventToolResult:
		m.handleToolResult(event)
	case agentic.EventTokenStats:
		m.handleTokenStats(event)
	case agentic.EventProgress:
		m.handleProgress(event)
	case agentic.EventEnd:
		m.handleEnd()
	case agentic.EventClear:
		m.handleClear()
	}
}

func (m *MessageLogObserver) handleStateChange(event agentic.OutputEvent) {
	m.state = event.State
	if event.State == agentic.StateThinking || event.State == agentic.StateContent {
		m.ensureCurrent()
		return
	}
	if event.State == agentic.StateIdle && m.current != nil {
		m.history = append(m.history, *m.current)
		m.current = nil
	}
}

func (m *MessageLogObserver) handleContent(event agentic.OutputEvent) {
	switch event.Role {
	case agentic.System, agentic.User:
		m.handleSystemUserContent(event)
	case agentic.ToolRole:
		m.handleToolRoleContent(event)
	default:
		m.handleAssistantContent(event)
	}
}

func (m *MessageLogObserver) handleSystemUserContent(event agentic.OutputEvent) {
	m.history = append(m.history, StructuredMessage{
		Role: string(event.Role),
		Elements: []MessageElement{
			{Type: "content", Text: event.Text},
		},
	})
	// Discard empty current message if it was created by a state change
	// for system/user content routed through the bus.
	if m.current != nil && len(m.current.Elements) == 0 {
		m.current = nil
	}
}

func (m *MessageLogObserver) handleToolRoleContent(event agentic.OutputEvent) {
	m.history = append(m.history, StructuredMessage{
		Role: "tool",
		Elements: []MessageElement{
			{Type: "tool_result", Text: event.Text, ToolCallID: event.ToolCallID},
		},
	})
	m.attachToolResult(event.ToolCallID, event.Text)
}

func (m *MessageLogObserver) handleAssistantContent(event agentic.OutputEvent) {
	m.ensureCurrent()
	elemType := contentElementType(m.state)
	if last := m.lastElement(); last != nil && last.Type == elemType {
		last.Text += event.Text
		return
	}
	m.current.Elements = append(m.current.Elements, MessageElement{Type: elemType, Text: event.Text})
}

func contentElementType(state agentic.OutputState) string {
	switch state {
	case agentic.StateThinking:
		return "thinking"
	case agentic.StateToolResult:
		return "tool_result"
	default:
		return "content"
	}
}

func (m *MessageLogObserver) lastElement() *MessageElement {
	if m.current == nil || len(m.current.Elements) == 0 {
		return nil
	}
	return &m.current.Elements[len(m.current.Elements)-1]
}

func (m *MessageLogObserver) handleToolCall(event agentic.OutputEvent) {
	m.ensureCurrent()
	m.current.Elements = append(m.current.Elements, MessageElement{
		Type:       "tool_call",
		ToolName:   event.ToolName,
		ToolInput:  event.ToolInput,
		ToolCallID: event.ToolCallID,
	})
	m.pendingToolCalls[event.ToolCallID] = &m.current.Elements[len(m.current.Elements)-1]
}

func (m *MessageLogObserver) handleToolResult(event agentic.OutputEvent) {
	m.history = append(m.history, StructuredMessage{
		Role: "tool",
		Elements: []MessageElement{
			{Type: "tool_result", Text: event.Text, ToolCallID: event.ToolCallID},
		},
	})
	m.attachToolResult(event.ToolCallID, event.Text)
}

func (m *MessageLogObserver) attachToolResult(toolCallID, text string) {
	if elem, ok := m.pendingToolCalls[toolCallID]; ok {
		elem.ToolResult = text
		delete(m.pendingToolCalls, toolCallID)
	}
}

func (m *MessageLogObserver) handleTokenStats(event agentic.OutputEvent) {
	m.ensureCurrent()
	m.current.Timings = event.Timings
}

func (m *MessageLogObserver) handleProgress(event agentic.OutputEvent) {
	m.ensureCurrent()
	m.current.PromptProgress = event.PromptProgress
}

func (m *MessageLogObserver) handleEnd() {
	if m.current != nil {
		m.history = append(m.history, *m.current)
		m.current = nil
	}
}

func (m *MessageLogObserver) handleClear() {
	m.history = nil
	m.current = nil
	m.pendingToolCalls = make(map[string]*MessageElement)
	m.state = agentic.StateIdle
}

func (m *MessageLogObserver) ensureCurrent() {
	if m.current == nil {
		m.current = &StructuredMessage{Role: "assistant", Elements: []MessageElement{}}
	}
}

// History returns the full conversation history, including any in-progress message.
func (m *MessageLogObserver) History() []StructuredMessage {
	result := make([]StructuredMessage, len(m.history))
	copy(result, m.history)
	if m.current != nil {
		result = append(result, *m.current)
	}
	return result
}

// JSON returns the conversation history as a JSON byte slice.
func (m *MessageLogObserver) JSON() ([]byte, error) {
	return json.Marshal(m.History())
}
