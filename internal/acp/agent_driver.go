// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package acp

import "github.com/pijalu/goa/internal/agentic"

// AgentDriver drives a real Goa agent session for ACP.
// Implementations start the session, send user input, and stream events.
type AgentDriver interface {
	// StartSession initializes the agent session and begins emitting events.
	StartSession() error
	// SendUserInput sends a user message to the active session.
	SendUserInput(input string) error
	// Events returns the channel of agent output events.
	Events() <-chan agentic.OutputEvent
	// Interrupt cancels any in-progress generation.
	Interrupt()
}
