// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox

import "context"

// Mode is the confinement level for a sandboxed tool call. The vocabulary
// mirrors the dsh bash-sandbox modes (packages/shell/bash-sandbox): the mode
// names are the model-facing contract, so they are stable strings, not
// internal identifiers.
//
// Confinement in goa covers file effects only (project-directory jail);
// network and process visibility are deliberately not restricted — the modes
// do not pretend to cover what the backend does not enforce.
type Mode string

const (
	// ModeReadOnly permits no writes anywhere. It is part of the closed
	// vocabulary so widening ranks are stable; the goa bash jail currently
	// never runs in this mode.
	ModeReadOnly Mode = "read-only"
	// ModeWorkspaceWrite confines writes to the workspace root. This is the
	// effective mode of the goa project-directory jail: commands run confined
	// to the project directory (plus the jail's scratch allowances).
	ModeWorkspaceWrite Mode = "workspace-write"
	// ModeDangerFullAccess disables confinement entirely. It is an explicit
	// unconfined mode, not a wider sandbox profile.
	ModeDangerFullAccess Mode = "danger-full-access"
)

// Rank returns the confinement strictness rank. Higher ranks grant strictly
// more access. Unknown modes rank 0 (invalid).
func (m Mode) Rank() int {
	switch m {
	case ModeReadOnly:
		return 1
	case ModeWorkspaceWrite:
		return 2
	case ModeDangerFullAccess:
		return 3
	}
	return 0
}

// IsValid reports whether m is a known mode.
func (m Mode) IsValid() bool { return m.Rank() > 0 }

// StrictlyWider reports whether m grants strictly more access than other.
// Equal modes and unknown modes are never strictly wider.
func (m Mode) StrictlyWider(other Mode) bool {
	return m.IsValid() && other.IsValid() && m.Rank() > other.Rank()
}

// EscalationVocabulary is the closed target vocabulary advertised to the
// model for sandbox escalation. It is always the full set — never cut down to
// the executor's default mode; strict widening is checked at execution time
// against the call's effective mode, and a non-widening request fails without
// prompting anyone.
var EscalationVocabulary = []string{string(ModeWorkspaceWrite), string(ModeDangerFullAccess)}

// EscalationRequest carries the context of a sandbox escalation approval.
// The approval path (see internal/perms and the app-level confirmTool wiring)
// uses it to decide whether an escalated (wider) command may run.
type EscalationRequest struct {
	// ToolName is the tool requesting escalation ("bash").
	ToolName string
	// Command is the exact shell command that was denied and would run wider.
	Command string
	// Workdir is the requested working directory (may be empty).
	Workdir string
	// CurrentMode is the mode the command was denied under.
	CurrentMode Mode
	// RequestedMode is the strictly wider mode requested for this call.
	RequestedMode Mode
	// Justification is the model-supplied one-sentence explanation for the
	// user. Required together with sandbox_permissions.
	Justification string
}

// EscalationApprover approves or rejects a sandbox escalation before an
// escalated command runs. It returns (true, nil) to allow the exact command
// under the requested wider mode, or (false, nil) / an error to keep the
// denial final. A nil approver (unwired composition) denies escalation.
type EscalationApprover func(ctx context.Context, req EscalationRequest) (bool, error)
