// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

import "time"

// Delegation lifecycle states emitted on the orchestrator event feed as
// OrchestratorMessage{Kind: "delegation_state"} with Content "<state>|<detail>"
// (pipe-encoded, mirroring the TASK_STATUS/GATE_APPROVAL precedent). They
// bracket a delegated run so a downstream consumer (the multi-agent TUI) can
// spawn a per-delegation view the moment the delegation is created and mark
// it terminal — success or FAILED — when it ends. The detail field carries
// the error text for DelegationFailed (the error card body) and is empty
// otherwise.
const (
	// DelegationRunning is emitted right before the delegated agent starts
	// its run (the PENDING→RUNNING edge of specs/async-delegation.md).
	DelegationRunning = "running"
	// DelegationCompleted is emitted after the delegated run finishes
	// successfully.
	DelegationCompleted = "completed"
	// DelegationFailed is emitted after the delegated run aborts with an
	// error; the detail carries the error text.
	DelegationFailed = "failed"
)

// EmitDelegationState emits one delegation lifecycle message for delegationID
// on the orchestrator's event feed, stamped with the delegation id so the
// consumer can attribute it to the right per-delegation view. It is the
// visibility fix for the "delegate_to is invisible" bug: a delegation now
// always produces a creation marker and a terminal marker, even when the run
// itself streams nothing (e.g. the provider fails before the first chunk).
//
// The send matches emitKind's semantics for non-chunk messages (blocking on
// the buffered events channel); callers invoke it on the tool's goroutine
// around subAgent.Run, never from the TUI loop.
func (o *ForegroundOrchestrator) EmitDelegationState(role, delegationID, state, detail string) {
	if o == nil || delegationID == "" {
		return
	}
	content := state + "|" + detail
	o.events <- OrchestratorMessage{
		From:         role,
		To:           "delegation",
		Kind:         "delegation_state",
		Content:      content,
		DelegationID: delegationID,
		Timestamp:    time.Now(),
	}
}
