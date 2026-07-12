// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

// Emit sends a plain orchestrator message to the event stream. Exposed so
// external tools (e.g. the agent sub-agent tool) can announce their lifecycle
// without being coupled to the orchestrator's internal emit signature.
func (o *ForegroundOrchestrator) Emit(from, to, content string) {
	o.emit(from, to, content)
}
