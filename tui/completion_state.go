// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package tui provides the terminal UI for Goa.
package tui

// CompPhase represents the phase of the completion state machine.
// Completion transitions from Command (typing /cmd) through Accepted (Tab/Enter)
// to Arg (typing :arg), following the A2 redesign.
type CompPhase int

const (
	PhaseInactive CompPhase = iota
	PhaseCommand            // Completing command names: /m → /mode, /memory
	PhaseAccepted           // Command accepted, showing param variants: /memory → /memory:, /memory:clear
	PhaseArg                // Completing argument values: /memory:cl → /memory:clear
)

// CompState holds the full completion state for both Editor and Input.
// Replaces the ad-hoc compActive/compItems/compIdx/compPrefix fields.
type CompState struct {
	Phase   CompPhase
	Items   []Completion
	Idx     int
	Prefix  string   // raw text being completed
	CmdName string   // e.g. "memory" (without leading /)
	ArgPath []string // accumulated args for nested completion
	Trigger string   // "regular" | "force" | ""
	// UserNavigated is true once the user has explicitly moved the popup
	// selection (Up/Down). It resets when the popup is cleared or refreshed
	// by further typing, so an un-navigated Enter still submits text as-typed.
	UserNavigated bool
}

// Active returns true if the completion popup should be shown.
func (cs *CompState) Active() bool {
	return cs.Phase != PhaseInactive && len(cs.Items) > 0
}

// Clear resets the completion state to inactive.
func (cs *CompState) Clear() {
	cs.Phase = PhaseInactive
	cs.Items = nil
	cs.Idx = 0
	cs.Prefix = ""
	cs.CmdName = ""
	cs.ArgPath = nil
	cs.Trigger = ""
	cs.UserNavigated = false
}

// Cycle moves the selection by delta (-1 or +1), wrapping around. It marks
// the state as user-navigated so Enter accepts the highlighted item.
func (cs *CompState) Cycle(delta int) {
	if len(cs.Items) == 0 {
		return
	}
	n := len(cs.Items)
	cs.Idx = (cs.Idx + delta + n) % n
	cs.UserNavigated = true
}

// Selected returns the currently selected completion item, or nil.
func (cs *CompState) Selected() *Completion {
	if cs.Idx < 0 || cs.Idx >= len(cs.Items) {
		return nil
	}
	return &cs.Items[cs.Idx]
}
