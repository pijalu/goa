// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package agentctx

// AgentView is one agent's view context: its transcript (the live entries)
// plus its saved compositor state. The two travel together so the registry
// can detach one agent and reattach another, restoring each agent's exact
// prior frame. Inactive views are pure data — they perform no screen writes.
type AgentView struct {
	Transcript *AgentTranscript
	Compositor *AgentCompositor
}

// AgentViewRegistry keys AgentViews by agent id and tracks which one is
// active (currently mounted/rendered). It is minimal in T1: it holds exactly
// one view — the main agent's — which is always the active view. The
// add/remove/cycle/select machinery for real multi-agent switching arrives in
// T2; here the registry only needs to own the main view so the app constructs
// the main agent through it.
//
// Ownership (R1): like the components it references, the registry is mutated
// only on the TUI command loop, so it needs no internal locking.
type AgentViewRegistry struct {
	views  map[string]*AgentView
	active string
}

// NewAgentViewRegistry returns an empty registry (no views, no active view).
func NewAgentViewRegistry() *AgentViewRegistry {
	return &AgentViewRegistry{views: map[string]*AgentView{}}
}

// Add registers (or replaces) a view for id. The first view added becomes the
// active view; later additions (T2+) do not steal focus. Adding the already
// active id leaves the active pointer unchanged.
func (r *AgentViewRegistry) Add(id string, v *AgentView) {
	if r.views == nil {
		r.views = map[string]*AgentView{}
	}
	if _, exists := r.views[id]; !exists && r.active == "" {
		r.active = id
	}
	r.views[id] = v
}

// Get returns the view registered for id, or (nil, false) when absent.
func (r *AgentViewRegistry) Get(id string) (*AgentView, bool) {
	v, ok := r.views[id]
	return v, ok
}

// Active returns the id and view of the currently active agent, or ("", nil)
// when the registry is empty. In T1 this is always the main view.
func (r *AgentViewRegistry) Active() (string, *AgentView) {
	v, ok := r.views[r.active]
	if !ok {
		return "", nil
	}
	return r.active, v
}

// Len returns the number of registered views.
func (r *AgentViewRegistry) Len() int { return len(r.views) }
