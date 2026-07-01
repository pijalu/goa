// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package common

import (
	"strings"

	"github.com/pijalu/goa/internal/agentic"
)

// ToolGroup tracks a set of tools registered under a shared namespace prefix.
// It is used by MCP clients and plugins so that an entire group can be
// unregistered in one call when the connection or plugin goes away.
type ToolGroup struct {
	Prefix string
	Tools  []agentic.Tool
}

// Names returns the tool names in the group.
func (g *ToolGroup) Names() []string {
	names := make([]string, len(g.Tools))
	for i, t := range g.Tools {
		names[i] = t.Schema().Name
	}
	return names
}

// Match reports whether name belongs to this group based on the prefix.
func (g *ToolGroup) Match(name string) bool {
	return strings.HasPrefix(name, g.Prefix)
}
