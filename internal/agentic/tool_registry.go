// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"sort"
	"sync"
)

// ToolRegistry manages the collection of tools available to an Agent.
// It provides lookup by name and schema aggregation for LLM requests.
// A ToolRegistry is immutable after construction (SetTools builds a fresh one),
// so the computed schema list is cached after the first call.
type ToolRegistry struct {
	tools    map[string]Tool
	once     sync.Once
	cached   []ToolSchema
	hintsOnce sync.Once
	cachedHints map[string]ToolLoopHints
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
//
// The result is computed once and cached: the agent calls this every stream
// round and retry, and each tool's Schema() allocates fresh maps, so a fresh
// re-sort + re-allocation per round is wasteful. Callers must treat the
// returned slice as read-only.
func (r *ToolRegistry) Schemas() []ToolSchema {
	r.once.Do(func() {
		keys := make([]string, 0, len(r.tools))
		for k := range r.tools {
			keys = append(keys, k)
		}
		sort.Strings(keys)

		r.cached = make([]ToolSchema, len(keys))
		for i, k := range keys {
			r.cached[i] = r.tools[k].Schema()
		}
	})
	return r.cached
}

// LoopHints returns the LoopAnnotated metadata for every tool that supplies it.
// The result is computed once and cached (a ToolRegistry is immutable after
// construction). Used by the tool-loop controller so it can stay name-agnostic.
func (r *ToolRegistry) LoopHints() map[string]ToolLoopHints {
	r.hintsOnce.Do(func() {
		r.cachedHints = make(map[string]ToolLoopHints, len(r.tools))
		for name, t := range r.tools {
			if la, ok := t.(LoopAnnotated); ok {
				r.cachedHints[name] = la.LoopHints()
			}
		}
	})
	return r.cachedHints
}
