// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package catalog

import "strings"

// Sanitize maps an MCP server or tool name onto the safe identifier charset
// [a-zA-Z0-9_-], replacing every other byte with '_'. MCP tool names become
// LLM tool identifiers, which many providers restrict to that charset.
func Sanitize(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); i++ {
		c := s[i]
		if (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '_' || c == '-' {
			b.WriteByte(c)
		} else {
			b.WriteByte('_')
		}
	}
	return b.String()
}

// ToolName builds the agent-facing tool name for one MCP tool:
// mcp__<sanitized-server>__<sanitized-tool>. The double-underscore scheme is
// Goa's existing, collision-resistant namespace (kept over OpenCode's single
// underscore so a server named e.g. "a_b" cannot collide with server "a",
// tool "b").
func ToolName(server, tool string) string {
	return "mcp__" + Sanitize(server) + "__" + Sanitize(tool)
}

// ToolPrefix returns the namespace prefix for all of a server's tools, used
// for group registration/unregistration: mcp__<sanitized-server>__.
func ToolPrefix(server string) string {
	return "mcp__" + Sanitize(server) + "__"
}
