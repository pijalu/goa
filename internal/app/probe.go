// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"github.com/pijalu/goa/config"
)

// ProbeSubsystems builds the full subsystem graph for diagnostics without
// starting a session. Used by e2e/probe to inspect the production wiring
// (config cascade → tool registry) for a project directory.
func ProbeSubsystems(cfg *config.Config, loader *config.CascadeLoader, projectDir string) *subsystems {
	return InitSubsystems(cfg, loader, projectDir, RuntimeOptions{PromptArg: "probe", Plain: true})
}

// ProbeToolNames returns the schema names of every registered tool.
func (s *subsystems) ProbeToolNames() []string {
	if s == nil || s.toolRegistry == nil {
		return nil
	}
	var names []string
	for _, t := range s.toolRegistry.All() {
		names = append(names, t.Schema().Name)
	}
	return names
}

// ProbeAgentDrivenToolState reports whether the agent-driven companion tools
// (request_review, delegate_to) are registered AND execution-enabled after
// full subsystem wiring, including session-state restore. Registration makes
// the tool visible to the model; Enabled gates execution (bugs.md F5).
func (s *subsystems) ProbeAgentDrivenToolState() (rrRegistered, rrEnabled, dtRegistered, dtEnabled bool) {
	if s == nil {
		return false, false, false, false
	}
	if rrRegistered = s.requestReviewTool != nil; rrRegistered {
		rrEnabled = s.requestReviewTool.Enabled
	}
	if dtRegistered = s.delegateTool != nil; dtRegistered {
		dtEnabled = s.delegateTool.Enabled
	}
	return rrRegistered, rrEnabled, dtRegistered, dtEnabled
}
