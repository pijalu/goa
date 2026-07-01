// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

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

// UIBridge provides a JS API for plugins to add UI elements.
// Registered via ExtensionRegistry when plugins are loaded.
type UIBridge struct {
	panes    []UIPaneDef
	segments []UISegmentDef
}

// UIDialogDef defines a modal dialog.
type UIDialogDef struct {
	ID     string
	Title  string
	Render func() string
}

// NewUIBridge creates a new UI bridge.
func NewUIBridge() *UIBridge {
	return &UIBridge{}
}

// AddPane registers a plugin pane.
func (b *UIBridge) AddPane(def UIPaneDef) {
	b.panes = append(b.panes, def)
}

// AddSegment registers a plugin mode line segment.
func (b *UIBridge) AddSegment(def UISegmentDef) {
	b.segments = append(b.segments, def)
}

// Panes returns all registered plugin panes.
func (b *UIBridge) Panes() []UIPaneDef {
	return b.panes
}

// Segments returns all registered plugin segments.
func (b *UIBridge) Segments() []UISegmentDef {
	return b.segments
}
