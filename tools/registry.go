// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"sort"
	"strings"

	"github.com/pijalu/goa/internal/agentic"
)

// ToolRegistry wraps agentic.ToolRegistry with Documentable lookup and group
// registration for dynamic tool namespaces (MCP, plugins).
type ToolRegistry struct {
	tools    map[string]agentic.Tool
	docTools map[string]Documentable // tools that implement Documentable
	groups   []*ToolGroup
}

// NewToolRegistry creates an empty tool registry.
func NewToolRegistry() *ToolRegistry {
	return &ToolRegistry{
		tools:    make(map[string]agentic.Tool),
		docTools: make(map[string]Documentable),
		groups:   nil,
	}
}

// RegisterGroup registers all tools under a shared namespace prefix.
func (r *ToolRegistry) RegisterGroup(prefix string, tools []agentic.Tool) {
	group := &ToolGroup{Prefix: prefix, Tools: tools}
	r.groups = append(r.groups, group)
	for _, t := range tools {
		r.Register(t)
	}
}

// UnregisterGroup removes all tools whose names match the prefix.
func (r *ToolRegistry) UnregisterGroup(prefix string) {
	for name := range r.tools {
		if strings.HasPrefix(name, prefix) {
			r.Unregister(name)
		}
	}
	filtered := r.groups[:0]
	for _, g := range r.groups {
		if g.Prefix != prefix {
			filtered = append(filtered, g)
		}
	}
	r.groups = filtered
}

// Match reports whether name matches any registered group prefix.
func (r *ToolRegistry) Match(name string) bool {
	for _, g := range r.groups {
		if g.Match(name) {
			return true
		}
	}
	return false
}

// Register adds a tool to the registry. If the tool implements Documentable,
// it's also registered for documentation lookup.
func (r *ToolRegistry) Register(tool agentic.Tool) {
	name := tool.Schema().Name
	r.tools[name] = tool
	if doc, ok := tool.(Documentable); ok {
		r.docTools[name] = doc
	}
}

// Get retrieves a tool by name.
func (r *ToolRegistry) Get(name string) (agentic.Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Unregister removes a tool from the registry.
func (r *ToolRegistry) Unregister(name string) {
	delete(r.tools, name)
	delete(r.docTools, name)
}

// All returns all registered tools in a stable order.
func (r *ToolRegistry) All() []agentic.Tool {
	names := sortedKeys(r.tools)
	result := make([]agentic.Tool, len(names))
	for i, name := range names {
		result[i] = r.tools[name]
	}
	return result
}

// AllDocumented returns all tools implementing Documentable.
func (r *ToolRegistry) AllDocumented() []DocumentedTool {
	result := make([]DocumentedTool, 0, len(r.docTools))
	for name, doc := range r.docTools {
		result = append(result, DocumentedTool{
			Tool:     r.tools[name],
			ShortDoc: doc.ShortDoc(),
			LongDoc:  doc.LongDoc(),
			Examples: doc.Examples(),
		})
	}
	return result
}

// ConfigurableToolNames returns the names of tools whose registration can
// be toggled at runtime through configuration.
func ConfigurableToolNames() []string {
	return []string{
		"bg_exec",
		"delegate_to",
		"memento",
		"pty_exec",
		"request_review",
		"ssh_bash",
		"webfetch",
	}
}

// sortedKeys returns the keys of a string-keyed map sorted alphabetically.
func sortedKeys(m map[string]agentic.Tool) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
