// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"sync"

	"github.com/pijalu/goa/internal/agentic"
)

// acpAgentDriver drives a real agent session for the ACP server.
type acpAgentDriver struct {
	subs      *subsystems
	sessionID string
	mu        sync.Mutex
	started   bool
}

// newACPAgentDriver creates a driver for the given ACP session.
func newACPAgentDriver(subs *subsystems, sessionID string) *acpAgentDriver {
	return &acpAgentDriver{subs: subs, sessionID: sessionID}
}

// StartSession initializes the real agent session.
func (d *acpAgentDriver) StartSession() error {
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.started {
		return nil
	}
	if d.subs.providerMgr == nil {
		return fmt.Errorf("no provider configured")
	}
	providerCfg, _ := d.subs.providerMgr.Active()
	if providerCfg == nil {
		return fmt.Errorf("no provider configured")
	}
	mdl, err := d.subs.providerMgr.ResolveActiveModel()
	if err != nil {
		return fmt.Errorf("failed to resolve model: %w", err)
	}
	streamOpts := d.subs.providerMgr.BuildStreamOptions()
	systemPrompt := buildSystemPrompt(d.subs)
	agenticTools := d.subs.toolRegistry.All()
	// ACP consumers read agent events from the internal channel, so enable
	// forwarding before starting the session.
	d.subs.agentMgr.SetForwardInternalEvents(true)
	if _, err := d.subs.agentMgr.StartSession(mdl, streamOpts, systemPrompt, agenticTools, d.subs.cfg); err != nil {
		return fmt.Errorf("failed to start session: %w", err)
	}
	if d.subs.foregroundOrch != nil {
		if mainAgent := d.subs.agentMgr.CurrentAgent(); mainAgent != nil {
			d.subs.foregroundOrch.SetMainAgent(mainAgent)
		}
	}
	d.started = true
	return nil
}

// SendUserInput sends a user message to the active agent session.
func (d *acpAgentDriver) SendUserInput(input string) error {
	return d.subs.agentMgr.SendUserInput(input)
}

// Events returns the channel of agent output events.
func (d *acpAgentDriver) Events() <-chan agentic.OutputEvent {
	return d.subs.agentMgr.Events()
}

// Interrupt cancels any in-progress generation.
func (d *acpAgentDriver) Interrupt() {
	d.subs.agentMgr.Interrupt()
}
