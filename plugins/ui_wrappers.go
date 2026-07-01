// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package plugins

import "fmt"

// JSPane wraps a JS-provided pane for the TUI.
type JSPane struct {
	ID       string
	Title    string
	renderFn func() string
}

// NewJSPane creates a JS pane wrapper.
func NewJSPane(id, title string, renderFn func() string) *JSPane {
	return &JSPane{
		ID:       id,
		Title:    title,
		renderFn: renderFn,
	}
}

// Render calls the JS render function with error recovery.
func (p *JSPane) Render() string {
	if p.renderFn == nil {
		return ""
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠ plugin error in pane %s: %v\n", p.ID, r)
		}
	}()
	return p.renderFn()
}

// JSSegment wraps a JS-provided mode line segment.
type JSSegment struct {
	ID       string
	Priority int
	renderFn func() string
}

// NewJSSegment creates a JS segment wrapper.
func NewJSSegment(id string, priority int, renderFn func() string) *JSSegment {
	return &JSSegment{
		ID:       id,
		Priority: priority,
		renderFn: renderFn,
	}
}

// Render calls the JS render function with error recovery.
func (s *JSSegment) Render() string {
	if s.renderFn == nil {
		return ""
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠ plugin error in segment %s: %v\n", s.ID, r)
		}
	}()
	return s.renderFn()
}

// JSModal wraps a JS-provided modal dialog.
type JSModal struct {
	ID       string
	Title    string
	renderFn func() string
}

// NewJSModal creates a JS modal wrapper.
func NewJSModal(id, title string, renderFn func() string) *JSModal {
	return &JSModal{
		ID:       id,
		Title:    title,
		renderFn: renderFn,
	}
}

// Render calls the JS render function with error recovery.
func (m *JSModal) Render() string {
	if m.renderFn == nil {
		return ""
	}
	defer func() {
		if r := recover(); r != nil {
			fmt.Printf("⚠ plugin error in modal %s: %v\n", m.ID, r)
		}
	}()
	return m.renderFn()
}
