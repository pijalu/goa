// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

import "github.com/pijalu/goa/internal/agentic"

// LiveToolSet returns the tool set a session would start with: the host-wired
// mode-filtered view (Context.LiveTools) when available, else the raw live
// registry. Command surfaces push this set to a running agent, so the agent
// always sees exactly what the next session start would see — a tool the
// active mode disallows can never be advertised by a runtime toggle.
func (c Context) LiveToolSet() []agentic.Tool {
	if c.LiveTools != nil {
		return c.LiveTools()
	}
	if c.ToolRegistry == nil {
		return nil
	}
	return c.ToolRegistry.All()
}
