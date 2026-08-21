// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentctx

// CompositorSnapshot is the serialized, per-agent compositor state needed to
// detach a transcript from the screen and later reattach it without a visible
// seam. It mirrors the three fields the single shared tui.Compositor carries
// for the one conversation it renders; multi-agent (T2+) saves one snapshot
// per agent so a view switch can restore the target's exact prior frame.
//
// It is pure data: capturing or holding a snapshot performs no terminal
// writes, so an inactive agent's compositor state is inert.
type CompositorSnapshot struct {
	// PrevLines is the previous frame's full visible-window baseline (the
	// unchanged-row skip source), copied so later frames cannot mutate it.
	PrevLines []string
	// ScrollTop is the scrollback watermark: canvas rows already committed to
	// terminal scrollback (immutable, never repainted).
	ScrollTop int
	// VT is the previous frame's viewport top (first visible canvas row).
	VT int
}

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
