// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import "sync"

// UIPaneDef defines a pane that a plugin wants to add to the TUI.
type UIPaneDef struct {
	ID     string
	Title  string
	Render func() string
}

// UISegmentDef defines a mode line segment a plugin wants to add.
type UISegmentDef struct {
	ID       string
	Priority int
	Render   func() string
}

// UIDialogDef defines a modal dialog.
type UIDialogDef struct {
	ID     string
	Title  string
	Render func() string
}

// UIBridge provides a JS API for plugins to add UI elements.
// Registered via ExtensionRegistry when plugins are loaded.
//
// The bridge is safe for concurrent use: plugins mutate it from the plugin
// runner while the TUI reads rendered segments from the render loop.
type UIBridge struct {
	mu       sync.RWMutex
	panes    []UIPaneDef
	segments []UISegmentDef
	modals   []UIDialogDef
	// refresh broadcasts segment re-render requests (goa.ui.refreshSegment)
	// to the TUI. Buffered so a plugin never blocks the runner on a render.
	refresh chan string
}

// NewUIBridge creates a new UI bridge.
func NewUIBridge() *UIBridge {
	return &UIBridge{refresh: make(chan string, 16)}
}

// AddPane registers a plugin pane.
func (b *UIBridge) AddPane(def UIPaneDef) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.panes = append(b.panes, def)
}

// AddSegment registers a plugin mode line segment.
func (b *UIBridge) AddSegment(def UISegmentDef) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.segments = append(b.segments, def)
}

// AddModal registers a plugin modal dialog.
func (b *UIBridge) AddModal(def UIDialogDef) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.modals = append(b.modals, def)
}

// Panes returns all registered plugin panes.
func (b *UIBridge) Panes() []UIPaneDef {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]UIPaneDef, len(b.panes))
	copy(out, b.panes)
	return out
}

// Segments returns all registered plugin segments.
func (b *UIBridge) Segments() []UISegmentDef {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]UISegmentDef, len(b.segments))
	copy(out, b.segments)
	return out
}

// Modals returns all registered plugin modals.
func (b *UIBridge) Modals() []UIDialogDef {
	b.mu.RLock()
	defer b.mu.RUnlock()
	out := make([]UIDialogDef, len(b.modals))
	copy(out, b.modals)
	return out
}

// RequestRefresh signals that a segment's rendered content changed. The
// notification is non-blocking; a saturated channel drops the oldest intent
// (the TUI re-renders the latest state anyway, so coalescing is safe).
func (b *UIBridge) RequestRefresh(segmentID string) {
	select {
	case b.refresh <- segmentID:
	default:
	}
}

// RefreshRequests returns the channel the TUI drains to learn about segment
// updates. May return nil-receiver-safe values; callers range/select on it.
func (b *UIBridge) RefreshRequests() <-chan string {
	return b.refresh
}
