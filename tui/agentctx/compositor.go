// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentctx

import "github.com/pijalu/goa/tui"

// CompositorSnapshot is the serialized, per-agent compositor state needed to
// detach a transcript from the screen and later reattach it without a visible
// seam. It mirrors the three fields the single shared tui.Compositor carries
// for the one conversation it renders; multi-agent (T2+) saves one snapshot
// per agent so a view switch can restore the target's exact prior frame.
//
// The canonical definition lives in the tui package (tui.FrameState — the
// compositor is its producer/consumer); this alias keeps the agentctx API
// stable. It is pure data: capturing or holding a snapshot performs no
// terminal writes, so an inactive agent's compositor state is inert.
type CompositorSnapshot = tui.FrameState

// AgentCompositor is a thin holder binding one CompositorSnapshot to one
// agent's transcript. In T1 the single main agent is always mounted, so the
// snapshot stays zero and inactive; it exists now so the registry/switching
// layers (T2/T3) have a stable per-agent home for saved frame state without
// reshaping this type later.
type AgentCompositor struct {
	snap CompositorSnapshot
}

// NewAgentCompositor returns an empty (zero) per-agent compositor holder.
func NewAgentCompositor() *AgentCompositor { return &AgentCompositor{} }

// Save records the given snapshot as this agent's saved compositor state.
func (ac *AgentCompositor) Save(s CompositorSnapshot) { ac.snap = s }

// Snapshot returns the saved compositor state (zero value if never saved).
func (ac *AgentCompositor) Snapshot() CompositorSnapshot { return ac.snap }
