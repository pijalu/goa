// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// NewAgentMessage creates a Component for rendering agent-to-agent messages.
func NewAgentMessage(text, agent string) Component {
	return &agentMessage{text: text, agent: agent}
}

// AddAgentMessage adds an agent-to-agent message to the chat viewport.
// The message is rendered with the agent's role-colored prefix.
func (cv *ChatViewport) AddAgentMessage(agent, content string) {
	msg := &ChatMessage{
		Type:    ConsoleAgentMessage,
		Content: content,
		Meta:    map[string]string{"agent": agent},
	}
	cv.AddMessage(msg)
}

// Ensure agentMessage implements Component (compile-time check).
var _ Component = (*agentMessage)(nil)
