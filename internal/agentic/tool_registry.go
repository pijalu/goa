// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import "sort"

// ToolRegistry manages the collection of tools available to an Agent.
// It provides lookup by name and schema aggregation for LLM requests.
type ToolRegistry struct {
	tools map[string]Tool
}

// NewToolRegistry creates a registry from a slice of tools.
func NewToolRegistry(tools []Tool) *ToolRegistry {
	m := make(map[string]Tool)
	for _, t := range tools {
		m[t.Schema().Name] = t
	}
	return &ToolRegistry{tools: m}
}

// Get retrieves a tool by name. Returns false if the tool is not registered.
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Schemas returns the ToolSchema for all registered tools in a stable,
// alphabetical order. This ensures repeated requests with the same tools
// produce identical payloads, which is required for prompt-cache hits.
func (r *ToolRegistry) Schemas() []ToolSchema {
	keys := make([]string, 0, len(r.tools))
	for k := range r.tools {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	out := make([]ToolSchema, len(keys))
	for i, k := range keys {
		out[i] = r.tools[k].Schema()
	}
	return out
}
