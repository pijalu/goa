// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

// Component is the base interface for all TUI components.
// Each component renders to a set of lines and can optionally receive keyboard input.
//
// Following pi's pattern, keyboard input is delivered as raw terminal strings
// which components can check using matchesKey().
type Component interface {
	// Render returns the lines this component produces at the given width.
	// Each line must not exceed width in visual columns.
	Render(width int) []string

	// HandleInput processes a raw terminal string key event.
	// Components should use matchesKey() to check for specific keys.
	HandleInput(data string)

	// Invalidate clears any cached rendering state.
	Invalidate()
}

// Focusable extends Component for components that can receive keyboard focus.
type Focusable interface {
	Component
	SetFocused(focused bool)
	Focused() bool
}

// KeyReleaseAware is an optional interface for components that want to receive
// key release events (Kitty protocol). Components that don't implement this
// have their release events filtered out.
type KeyReleaseAware interface {
	WantsKeyRelease() bool
}

// HeightAllocated is implemented by the layout's fill region (the conversation
// viewport). The TUI layout pass (buildScene) measures the fixed chrome,
// computes the remaining vertical slack (terminal height minus chrome), and
// pushes it to the fill via SetAllocatedHeight BEFORE rendering it. The fill
// bottom-anchors its content within that height (blank padding above the
// content) so that:
//   - the input/status/footer stay pinned at the screen bottom in every
//     regime (small content no longer floats the footer up), and
//   - growth scrolls the oldest content into scrollback instead of pushing
//     the footer / completed widgets downward.
//
// This replaces the former monotonically-growing stable-height padding, which
// leaked height across tab/filter changes and scrolled filtered content out of
// view. Single Responsibility: the layout owns the budget; the fill owns how to
// bottom-anchor within it.
type HeightAllocated interface {
	SetAllocatedHeight(height int)
}

// InputTrap is an optional interface for components that want to intercept
// global keys (Ctrl+C) before the TUI handles them.
type InputTrap interface {
	Component
	TrapInput(data string) bool // returns true if the component handled the key
}

// PopupRenderer is an optional interface for a base component that also
// produces a transient popup (e.g. the editor's autocomplete list). Returning
// the popup SEPARATELY from Render — instead of appending it to the rendered
// lines — is what keeps the popup from growing the base canvas.
//
// Why this matters: the Compositor pushes base canvas rows into terminal
// scrollback whenever the canvas grows taller than the terminal. A popup that
// is part of the base render therefore pushes the header/content into
// scrollback when it opens, and that content cannot be recovered when the
// popup closes (terminals cannot "un-scroll"). The TUI composites PopupLines
// as a LayerOverlay that floats above neighboring base content, so the base
// canvas height never changes and opening/closing a popup never touches
// scrollback.
type PopupRenderer interface {
	// PopupLines returns the transient popup lines for the given width, or
	// nil if no popup is currently active. Each line must not exceed width.
	PopupLines(width int) []string
}

// Container is a Component that arranges child components vertically.
// It delegates HandleInput to children that implement Focusable.
//
// Concurrency: the commandLoop is the sole owner of children. Every mutation
// (AddChild/RemoveChild/RemoveLastChild/Clear) and every iteration
// (Render/Invalidate/HandleInput/Children) happens on the loop, serialized by
// single ownership (serialized by the commandLoop). No mutex is required.
type Container struct {
	children []Component
}

// AddChild appends a child component.
func (c *Container) AddChild(child Component) {
	c.children = append(c.children, child)
}

// RemoveLastChild removes the last child from the container.
func (c *Container) RemoveLastChild() Component {
	if len(c.children) == 0 {
		return nil
	}
	last := c.children[len(c.children)-1]
	c.children = c.children[:len(c.children)-1]
	return last
}

// RemoveChild removes the first occurrence of a child by identity.
func (c *Container) RemoveChild(child Component) {
	for i, ch := range c.children {
		if ch == child {
			c.children = append(c.children[:i], c.children[i+1:]...)
			return
		}
	}
}

// Clear removes all children.
func (c *Container) Clear() { c.children = nil }

// Children returns a snapshot copy of the current children slice. Callers that
// need a stable view (e.g. iterating while the slice may be mutated) should use
// this instead of the unexported field.
func (c *Container) Children() []Component {
	snapshot := make([]Component, len(c.children))
	copy(snapshot, c.children)
	return snapshot
}

// Invalidate propagates to all children.
func (c *Container) Invalidate() {
	for _, child := range c.children {
		child.Invalidate()
	}
}

// Render concatenates all children's lines vertically.
func (c *Container) Render(width int) []string {
	var lines []string
	for _, child := range c.children {
		cl := child.Render(width)
		if cl != nil {
			lines = append(lines, cl...)
		}
	}
	return lines
}

// HandleInput delegates to focused children, stopping at the first that
// matches the key (all components receive the key if not focus-filtered).
// TUI handles focus routing externally, so Container just forwards to all.
func (c *Container) HandleInput(data string) {
	for _, child := range c.children {
		if f, ok := child.(Focusable); ok && f.Focused() {
			child.HandleInput(data)
			return
		}
	}
	// No focused child — send to first that handles it
	if len(c.children) > 0 {
		c.children[0].HandleInput(data)
	}
}
