// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package mcp

import "strings"

// sanitize maps an MCP server or tool name onto the safe identifier charset
// [a-zA-Z0-9_-], replacing every other byte with '_'. MCP tool names become
// LLM tool identifiers, which many providers restrict to that charset
// (OpenCode's McpCatalog.sanitize parity).
func sanitize(s string) string {
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

// normalizeSchema reshapes a tool's JSON Schema for the model: it forces
// type:"object", guarantees a properties map, and sets
// additionalProperties:false (OpenCode convertTool parity). The input is not
// mutated; a normalized copy is returned.
func normalizeSchema(in map[string]any) map[string]any {
	out := make(map[string]any, len(in)+3)
	for k, v := range in {
		out[k] = v
	}
	out["type"] = "object"
	if _, ok := out["properties"]; !ok {
		out["properties"] = map[string]any{}
	}
	out["additionalProperties"] = false
	return out
}
