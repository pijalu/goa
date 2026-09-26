// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools

import (
	"sort"
	"strings"
	"sync"

	"github.com/pijalu/goa/internal/agentic"
)

// ToolRegistry wraps agentic.ToolRegistry with Documentable lookup and group
// registration for dynamic tool namespaces (MCP, plugins).
//
// It is safe for concurrent use by multiple goroutines: every read is a
// snapshot taken under a read lock and released before any tool code runs
// (see All/AllDocumented/Match). Registered tools are written from the TUI
// goroutine (/config, /tools), MCP connect/disconnect, and plugin load, while
// the agent path reads the live registry on every request
// (ToolSearchTool.Schema/ExecuteWithResult → deferredTools → All).
//
// The lock must never be held across a call into a registered tool: a tool's
// Schema() can re-enter the registry (ToolSearchTool.Schema() derives its
// deferred-tool listing from the live registry via All), which would deadlock
// a non-reentrant mutex.
type ToolRegistry struct {
	mu       sync.RWMutex
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

// RegisterGroup registers all tools under a shared namespace prefix. The group
// entry is published first so a concurrent Match sees the namespace, then the
// tools are registered individually (Register is itself synchronized and
// resolves Schema() outside the lock).
func (r *ToolRegistry) RegisterGroup(prefix string, tools []agentic.Tool) {
	group := &ToolGroup{Prefix: prefix, Tools: tools}
	r.mu.Lock()
	r.groups = append(r.groups, group)
	r.mu.Unlock()
	for _, t := range tools {
		r.Register(t)
	}
}

// UnregisterGroup removes all tools whose names match the prefix.
func (r *ToolRegistry) UnregisterGroup(prefix string) {
	r.mu.RLock()
	names := make([]string, 0, len(r.tools))
	for name := range r.tools {
		if strings.HasPrefix(name, prefix) {
			names = append(names, name)
		}
	}
	r.mu.RUnlock()
	for _, name := range names {
		r.Unregister(name)
	}
	r.mu.Lock()
	kept := make([]*ToolGroup, 0, len(r.groups))
	for _, g := range r.groups {
		if g.Prefix != prefix {
			kept = append(kept, g)
		}
	}
	r.groups = kept
	r.mu.Unlock()
}

// Match reports whether name matches any registered group prefix.
func (r *ToolRegistry) Match(name string) bool {
	r.mu.RLock()
	groups := make([]*ToolGroup, len(r.groups))
	copy(groups, r.groups)
	r.mu.RUnlock()
	for _, g := range groups {
		if g.Match(name) {
			return true
		}
	}
	return false
}

// Register adds a tool to the registry. If the tool implements Documentable,
// it's also registered for documentation lookup. Schema() is resolved before
// the lock is taken: it may re-enter the registry.
func (r *ToolRegistry) Register(tool agentic.Tool) {
	name := tool.Schema().Name
	r.mu.Lock()
	r.tools[name] = tool
	if doc, ok := tool.(Documentable); ok {
		r.docTools[name] = doc
	}
	r.mu.Unlock()
}

// Get retrieves a tool by name.
func (r *ToolRegistry) Get(name string) (agentic.Tool, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	t, ok := r.tools[name]
	return t, ok
}

// Unregister removes a tool from the registry.
func (r *ToolRegistry) Unregister(name string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	delete(r.tools, name)
	delete(r.docTools, name)
}

// All returns all registered tools in a stable alphabetical order. The name →
// tool mapping is copied under the read lock and the lock is released before
// returning, so the caller iterates a snapshot that no concurrent
// Register/Unregister can mutate (an in-flight All never sees a half-updated
// map, and never observes a registration that happens while it runs).
func (r *ToolRegistry) All() []agentic.Tool {
	r.mu.RLock()
	defer r.mu.RUnlock()
	names := sortedKeys(r.tools)
	result := make([]agentic.Tool, len(names))
	for i, name := range names {
		result[i] = r.tools[name]
	}
	return result
}

// AllDocumented returns all tools implementing Documentable. The doc entries
// are snapshotted under the read lock; the document accessors are called after
// it is released (ShortDoc/LongDoc/Examples may re-enter the registry).
func (r *ToolRegistry) AllDocumented() []DocumentedTool {
	type docEntry struct {
		name string
		tool agentic.Tool
		doc  Documentable
	}
	r.mu.RLock()
	entries := make([]docEntry, 0, len(r.docTools))
	for name, doc := range r.docTools {
		entries = append(entries, docEntry{name: name, tool: r.tools[name], doc: doc})
	}
	r.mu.RUnlock()
	// Sorted by name: map iteration order is random, and callers (docs
	// rendering) must produce byte-stable output.
	sort.Slice(entries, func(i, j int) bool { return entries[i].name < entries[j].name })
	result := make([]DocumentedTool, len(entries))
	for i, e := range entries {
		result[i] = DocumentedTool{
			Tool:     e.tool,
			ShortDoc: e.doc.ShortDoc(),
			LongDoc:  e.doc.LongDoc(),
			Examples: e.doc.Examples(),
		}
	}
	return result
}

// ConfigurableTool describes a tool whose registration can be toggled at
// runtime through configuration. It is the single source of truth for the
// /config → Tools screen and the docs/on-off commands, so the list of
// toggleable tools lives in exactly one place.
type ConfigurableTool struct {
	Name        string
	Description string
	Default     bool // default enabled state (true = opt-out, false = opt-in)
}

// ConfigurableTools returns every runtime-toggleable tool with a short
// description and its default enabled state. The Default flags mirror the
// embedded default config (config/configs/default.yaml tools.enabled), so
// /config and /docs show the same on/off state a fresh install ships with.
// ask_user_question is Default:true (opt-out) — it is enabled by default and
// only removed when the user sets tools.enabled.clarify_disabled: true.
// goal is Default:true too: the embedded default ships tools.enabled.goal:true
// so autonomous goal `create` works out of the box; the flag only gates that
// one action (every other goal action works whenever a goal exists).
func ConfigurableTools() []ConfigurableTool {
	return []ConfigurableTool{
		{Name: "agent", Description: "Spawn a sub-agent for a task", Default: false},
		{Name: "agent_swarm", Description: "Fan out a swarm of sub-agents", Default: false},
		{Name: "goa", Description: "Run Goa slash commands from the model", Default: false},
		{Name: "verify", Description: "Run the test suite, report pass/fail", Default: false},
		{Name: "ask_user_question", Description: "Ask the user a question", Default: true},
		{Name: "python", Description: "Execute Python code with gpython", Default: true},
		{Name: "run_code", Description: "Python program with multi-tool dispatch (code-mode)", Default: true},
		{Name: "bg_exec", Description: "Background process execution", Default: false},
		{Name: "delegate_to", Description: "Delegate tasks to sub-agents", Default: false},
		{Name: "goal", Description: "Goal tracking", Default: true},
		{Name: "lsp", Description: "LSP code navigation", Default: false},
		{Name: "memento", Description: "Persistent memory files", Default: false},
		{Name: "terminals", Description: "Persistent terminal sessions", Default: true},
		{Name: "request_review", Description: "Request companion review", Default: false},
		{Name: "smartsearch", Description: "BM25 code search (needs restart)", Default: false},
		{Name: "ssh_bash", Description: "Remote SSH command execution", Default: false},
		{Name: "todo_list", Description: "Session todo list (goal-linked when a goal is active)", Default: false},
		{Name: "webfetch", Description: "URL content fetching", Default: true},
	}
}

// ConfigurableToolNames returns the names of tools whose registration can
// be toggled at runtime through configuration.
func ConfigurableToolNames() []string {
	list := ConfigurableTools()
	names := make([]string, len(list))
	for i, t := range list {
		names[i] = t.Name
	}
	return names
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
