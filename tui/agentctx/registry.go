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

	// activity/errFlag are the badge bookkeeping (rendered in T5): activity
	// records that the view accumulated rows or completed while inactive
	// (the ✱ badge); errFlag records a failure on the view (the ▲ badge).
	// Both are acknowledged — cleared — when the view becomes active, since
	// viewing the tab is what dismisses the notification.
	activity bool
	errFlag  bool
}

// AgentViewRegistry keys AgentViews by agent id and tracks which one is
// active (currently mounted/rendered). Views are kept in insertion order so
// tab cycling and the tab strip are stable and predictable.
//
// It is pure bookkeeping: the registry performs no terminal writes and never
// touches the engine tree. Mount/unmount, the compositor baseline swap, and
// the repaint request are the host's concern (internal/app), driven by the
// pointer moves here.
//
// Ownership (R1): like the components it references, the registry is mutated
// only on the TUI command loop, so it needs no internal locking.
type AgentViewRegistry struct {
	views  map[string]*AgentView
	order  []string // insertion order: tab strip order and cycle order
	active string
}

// NewAgentViewRegistry returns an empty registry (no views, no active view).
func NewAgentViewRegistry() *AgentViewRegistry {
	return &AgentViewRegistry{views: map[string]*AgentView{}}
}

// Add registers (or replaces) a view for id. The first view added becomes the
// active view; later additions do not steal focus. Re-adding an existing id
// replaces the view but keeps its position and badge state is owned by the
// NEW view (the old one's badges are discarded with it).
func (r *AgentViewRegistry) Add(id string, v *AgentView) {
	if r.views == nil {
		r.views = map[string]*AgentView{}
	}
	if _, exists := r.views[id]; !exists {
		r.order = append(r.order, id)
		if r.active == "" {
			r.active = id
		}
	}
	r.views[id] = v
}

// Remove deletes the view for id, reporting whether it existed. Removing the
// ACTIVE view hands the active pointer to the nearest remaining neighbor: the
// previous view in insertion order when one exists, else the next; the
// registry may be left empty (active ""). The removed view's Transcript is
// NOT unmounted here — detaching it from the screen is the host's concern.
func (r *AgentViewRegistry) Remove(id string) bool {
	if _, exists := r.views[id]; !exists {
		return false
	}
	delete(r.views, id)
	idx := -1
	for i, oid := range r.order {
		if oid == id {
			idx = i
			break
		}
	}
	if idx >= 0 {
		r.order = append(r.order[:idx], r.order[idx+1:]...)
	}
	if r.active == id {
		r.active = r.neighborAfterRemoval(idx)
	}
	return true
}

// neighborAfterRemoval picks the new active id after the view at order index
// idx was removed: prefer the view that now sits at idx-1 (the removed view's
// predecessor), else the one that slid into idx (its successor). "" when the
// registry is empty.
func (r *AgentViewRegistry) neighborAfterRemoval(idx int) string {
	switch {
	case idx > 0 && idx-1 < len(r.order):
		return r.order[idx-1]
	case idx >= 0 && idx < len(r.order):
		return r.order[idx]
	default:
		return ""
	}
}

// Get returns the view registered for id, or (nil, false) when absent.
func (r *AgentViewRegistry) Get(id string) (*AgentView, bool) {
	v, ok := r.views[id]
	return v, ok
}

// Active returns the id and view of the currently active agent, or ("", nil)
// when the registry is empty.
func (r *AgentViewRegistry) Active() (string, *AgentView) {
	v, ok := r.views[r.active]
	if !ok {
		return "", nil
	}
	return r.active, v
}

// Len returns the number of registered views.
func (r *AgentViewRegistry) Len() int { return len(r.views) }

// IDs returns the registered view ids in insertion (tab strip) order.
func (r *AgentViewRegistry) IDs() []string {
	out := make([]string, len(r.order))
	copy(out, r.order)
	return out
}

// Index returns the position of id in the tab order, or -1 when absent.
func (r *AgentViewRegistry) Index(id string) int {
	for i, oid := range r.order {
		if oid == id {
			return i
		}
	}
	return -1
}

// ActiveIndex returns the position of the active view in the tab order, or 0
// when the registry is empty.
func (r *AgentViewRegistry) ActiveIndex() int {
	if idx := r.Index(r.active); idx >= 0 {
		return idx
	}
	return 0
}

// Cycle moves the active pointer by dir steps (sign matters: +1 next, -1
// previous) through the insertion order, wrapping around, and returns the new
// active id and view. With fewer than two views it is a no-op returning the
// current active view. The newly activated view's badges are acknowledged.
func (r *AgentViewRegistry) Cycle(dir int) (string, *AgentView) {
	if len(r.order) < 2 || dir == 0 {
		return r.Active()
	}
	idx := r.ActiveIndex()
	step := 1
	if dir < 0 {
		step = -1
	}
	idx = (idx + step + len(r.order)) % len(r.order)
	return r.activate(r.order[idx])
}

// SelectByID makes id the active view and returns it. Unknown ids report
// (nil, false) and leave the active pointer unchanged. Selecting the already
// active view succeeds and is a no-op besides badge acknowledgement.
func (r *AgentViewRegistry) SelectByID(id string) (*AgentView, bool) {
	v, ok := r.views[id]
	if !ok {
		return nil, false
	}
	r.activate(id)
	return v, true
}

// activate moves the active pointer to id (which must exist) and acknowledges
// the view's badges: viewing a tab dismisses its activity/error notification.
func (r *AgentViewRegistry) activate(id string) (string, *AgentView) {
	r.active = id
	v := r.views[id]
	v.activity = false
	v.errFlag = false
	return id, v
}

// MarkActivity records background activity on the view (rows accumulated or a
// run completed while it was inactive) — the state behind the T5 ✱ badge.
// Unknown ids are ignored.
func (r *AgentViewRegistry) MarkActivity(id string) {
	if v, ok := r.views[id]; ok {
		v.activity = true
	}
}

// MarkError records a failure on the view — the state behind the T5 ▲ badge.
// Unknown ids are ignored.
func (r *AgentViewRegistry) MarkError(id string) {
	if v, ok := r.views[id]; ok {
		v.errFlag = true
	}
}

// Badges reports the view's unacknowledged badge state: (activity, error).
// Unknown ids report (false, false).
func (r *AgentViewRegistry) Badges(id string) (activity, errFlag bool) {
	v, ok := r.views[id]
	if !ok {
		return false, false
	}
	return v.activity, v.errFlag
}
