// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentic

import (
	"sort"
	"strings"
	"sync"
)

// deferralThreshold is the minimum number of deferred-eligible tools required
// for deferral to activate. Below it, the indirection cost (the tool_search
// loader + catalog) is not worth the bytes saved by withholding schemas.
const deferralThreshold = 8

// ToolLookup is the abstraction the agent depends on for resolving tools by
// name and producing stable, cache-friendly tool schemas. Depending on this
// interface (rather than a concrete registry) follows Dependency Inversion:// the agent no longer owns a parallel registry type, and any implementor
// (the canonical ToolRegistry below, or a mutable catalog in a higher layer)
// can supply the agent's working set.
type ToolLookup interface {
	// Get retrieves a tool by name.
	Get(name string) (Tool, bool)
	// Schemas returns the schemas of all tools in a stable, alphabetical
	// order suitable for prompt-cache hits. When deferred loading is active
	// the result is partitioned into a stable eager block + the tool_search
	// loader + an append-only loaded-tail (deferred tools the model has
	// pulled this session).
	Schemas() []ToolSchema
	// LoopHints returns LoopAnnotated metadata for tools that supply it.
	LoopHints() map[string]ToolLoopHints
	// LoadDeferred exposes deferred tools by name, appending their schemas to
	// the loaded-tail in load order (cache-stable: the eager block never
	// reorders, only the tail grows). Unknown, non-deferred, and already
	// loaded names are skipped. Returns the names actually loaded.
	LoadDeferred(names []string) []string
	// DeferredStatus reports whether name names a deferred tool that has not
	// yet been loaded, and the loader tool name for the redirect hint.
	// unloaded is false for eager tools, unknown names, and loaded tools.
	DeferredStatus(name string) (loaderName string, unloaded bool)
}

// ToolRegistry manages the collection of tools available to an Agent.
// It provides lookup by name and schema aggregation for LLM requests.
// A ToolRegistry is immutable after construction (SetTools builds a fresh one)
// except for the deferred-loading loaded-tail, which is append-only and
// mutex-protected.
type ToolRegistry struct {
	tools       map[string]Tool
	once        sync.Once
	cached      []ToolSchema
	hintsOnce   sync.Once
	cachedHints map[string]ToolLoopHints

	// Deferred-loading state (P1): configured once at construction.
	deferralActive bool
	loaderName     string            // loader tool name ("" when no loader)
	deferred       []string          // deferred tool names, alpha-sorted
	deferredSet    map[string]bool   // deferred name → true

	// Loaded-tail state: append-only, mutated by LoadDeferred.
	mu            sync.Mutex
	loadedSet     map[string]bool
	loadedOrder   []string
	loadedSchemas []ToolSchema
}

// NewToolRegistry creates a registry from a slice of tools.
func NewToolRegistry(tools []Tool) *ToolRegistry {
	m := make(map[string]Tool)
	for _, t := range tools {
		m[t.Schema().Name] = t
	}
	r := &ToolRegistry{
		tools:       m,
		deferredSet: make(map[string]bool),
		loadedSet:   make(map[string]bool),
	}
	r.configureDeferral()
	return r
}

// configureDeferral partitions the tool set into eager vs deferred and records
// the loader. Deferral activates only when BOTH conditions hold: (1) a
// DeferredToolLoader is part of the exposed tool set (the "loader runnable in
// the current permission mode" gate — a mode-filtered tool set simply has no
// loader, so nothing is withheld), and (2) the deferred-eligible count meets
// the threshold. Otherwise every schema ships eagerly, which is the
// pre-deferral behavior.
func (r *ToolRegistry) configureDeferral() {
	var loaderName string
	for name, t := range r.tools {
		if l, ok := t.(DeferredToolLoader); ok && l.IsDeferredToolLoader() {
			loaderName = name
			break
		}
	}
	if loaderName == "" {
		return
	}
	var deferred []string
	for name, t := range r.tools {
		if d, ok := t.(Deferred); ok && d.Deferred() {
			deferred = append(deferred, name)
			r.deferredSet[name] = true
		}
	}
	if len(deferred) < deferralThreshold {
		return
	}
	sort.Strings(deferred)
	r.deferralActive = true
	r.loaderName = loaderName
	r.deferred = deferred
}

// Get retrieves a tool by name. Returns false if the tool is not registered.
// Deferred-but-unloaded tools remain reachable here so the MCP publisher and
// access-resolution paths keep working; execution gating happens in the agent
// loop via DeferredStatus.
func (r *ToolRegistry) Get(name string) (Tool, bool) {
	t, ok := r.tools[name]
	return t, ok
}

// Schemas returns the ToolSchema for all registered tools in a stable,
// alphabetical order. This ensures repeated requests with the same tools
// produce identical payloads, which is required for prompt-cache hits.
//
// When deferred loading is active the result is partitioned into three
// regions, cache-stable across loads:
//
//  1. eager block: every non-deferred tool (core tools), alpha-sorted;
//  2. the tool_search loader (always present, compact catalog);
//  3. loaded-tail: deferred tools the model has pulled this session, in load
//     order (append-only).
//
// Two requests before/after a load differ only in the appended tail, so the
// provider prefix cache stays warm.
//
// The eager prefix is computed once and cached; the loaded-tail is appended
// per call. Callers must treat the returned slice as read-only.
func (r *ToolRegistry) Schemas() []ToolSchema {
	r.once.Do(func() { r.buildSchemas() })
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.loadedOrder) == 0 {
		return r.cached
	}
	out := make([]ToolSchema, 0, len(r.cached)+len(r.loadedSchemas))
	out = append(out, r.cached...)
	out = append(out, r.loadedSchemas...)
	return out
}

// buildSchemas computes the cached prefix: either the full alpha-sorted set
// (deferral inactive) or the eager block + loader (deferral active).
func (r *ToolRegistry) buildSchemas() {
	if !r.deferralActive {
		keys := make([]string, 0, len(r.tools))
		for k := range r.tools {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		r.cached = make([]ToolSchema, len(keys))
		for i, k := range keys {
			r.cached[i] = r.tools[k].Schema()
		}
		return
	}
	eager := make([]string, 0, len(r.tools))
	for k := range r.tools {
		if k == r.loaderName || r.deferredSet[k] {
			continue
		}
		eager = append(eager, k)
	}
	sort.Strings(eager)
	r.cached = make([]ToolSchema, 0, len(eager)+1)
	for _, k := range eager {
		r.cached = append(r.cached, r.tools[k].Schema())
	}
	// The loader always follows the eager block: a fixed position keeps the
	// eager region byte-stable regardless of which tools load.
	r.cached = append(r.cached, r.tools[r.loaderName].Schema())
}

// AllSchemas returns the schemas of EVERY registered tool, deferred or not,
// in stable alphabetical order. This is the full tool surface for consumers
// that must not see the deferred partition (e.g. the MCP tool publisher,
// which exposes goa's complete tool set over MCP regardless of what the LLM
// request ships).
func (r *ToolRegistry) AllSchemas() []ToolSchema {
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

// LoadDeferred exposes deferred tools by name, appending their schemas to the
// loaded-tail in the given order. Names that are unknown, not deferred, or
// already loaded are skipped. Returns the names actually loaded.
func (r *ToolRegistry) LoadDeferred(names []string) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.deferralActive {
		return nil
	}
	loaded := make([]string, 0, len(names))
	for _, n := range names {
		n = strings.TrimSpace(n)
		if !r.deferredSet[n] || r.loadedSet[n] {
			continue
		}
		t, ok := r.tools[n]
		if !ok {
			continue
		}
		r.loadedSet[n] = true
		r.loadedOrder = append(r.loadedOrder, n)
		r.loadedSchemas = append(r.loadedSchemas, t.Schema())
		loaded = append(loaded, n)
	}
	return loaded
}

// DeferredStatus reports whether name names a deferred tool that has not yet
// been loaded, and the loader tool name for the redirect hint. unloaded is
// false for eager tools, unknown names, and already-loaded tools.
func (r *ToolRegistry) DeferredStatus(name string) (loaderName string, unloaded bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.deferralActive || !r.deferredSet[name] || r.loadedSet[name] {
		return "", false
	}
	return r.loaderName, true
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

// Compile-time assurance that ToolRegistry satisfies the ToolLookup
// abstraction the agent depends on.
var _ ToolLookup = (*ToolRegistry)(nil)
