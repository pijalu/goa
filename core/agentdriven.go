// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import "sync"

// AgentDrivenGate tracks whether agent-driven tools (request_review / delegate_to)
// are enabled and stores the system-prompt addition injected when enabled.
type AgentDrivenGate struct {
	mu       sync.Mutex
	enabled  bool
	prompt   string
	onChange func(bool)
}

// NewAgentDrivenGate creates a disabled agent-driven gate.
func NewAgentDrivenGate() *AgentDrivenGate {
	return &AgentDrivenGate{}
}

// Enabled reports whether agent-driven tools are active.
func (g *AgentDrivenGate) Enabled() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.enabled
}

// SetEnabled updates the enabled state and notifies the change callback.
func (g *AgentDrivenGate) SetEnabled(enabled bool) {
	g.mu.Lock()
	changed := g.enabled != enabled
	g.enabled = enabled
	cb := g.onChange
	g.mu.Unlock()

	if changed && cb != nil {
		cb(enabled)
	}
}

// SetChangeCallback registers a callback invoked when the enabled state changes.
func (g *AgentDrivenGate) SetChangeCallback(cb func(bool)) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.onChange = cb
}

// Prompt returns the current agent-driven system-prompt addition.
func (g *AgentDrivenGate) Prompt() string {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.prompt
}

// SetPrompt stores the agent-driven system-prompt addition.
func (g *AgentDrivenGate) SetPrompt(p string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.prompt = p
}
