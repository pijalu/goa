// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package tuirender defines the interface between tool implementations and the
// TUI renderer. It lives in internal/tuirender so tool packages can implement
// renderers without creating an import cycle with the tui package.
package tuirender

// ToolRenderer produces the visual representation of a tool call and its
// result. Analogous to ToolDefinition.renderCall and renderResult hooks.
type ToolRenderer interface {
	// RenderCall returns the header text for the tool call. The returned
	// string should already be ANSI-styled.
	RenderCall(args map[string]any, ctx RenderContext) string

	// RenderResult returns the body text for the tool result. The returned
	// string may contain newlines and ANSI styling.
	RenderResult(output string, ctx RenderContext) string

	// PreviewLines returns the maximum number of output lines to show when the
	// component is collapsed. Return 0 to hide all output when collapsed.
	PreviewLines() int

	// HideResultWhenCollapsed returns true if the result body should be hidden
	// entirely when the component is collapsed (e.g. read).
	HideResultWhenCollapsed() bool
}

// DefaultBackgroundRenderer is an optional interface a ToolRenderer can
// implement to request that its tool execution box be rendered with the
// terminal's default background color instead of the status-colored
// background for raw bash/terminal output styling.
type DefaultBackgroundRenderer interface {
	DefaultBackground() bool
}

// StreamingRenderer is an optional extension for ToolRenderer. Tools whose
// arguments are useful while still streaming can implement RenderPartial;
// the TUI will show the returned body preview as soon as the partial arguments
// contain something worth displaying. This keeps the UI responsive without
// each renderer needing to overload RenderResult for the streaming case.
type StreamingRenderer interface {
	ToolRenderer
	// RenderPartial renders a preview of the tool body using the arguments
	// received so far. It should be compact and return an empty string when
	// there is nothing useful to show yet.
	RenderPartial(args map[string]any, ctx RenderContext) string
}

// RenderContext carries execution-state information passed to renderers.
type RenderContext struct {
	// Cwd is the current working directory.
	Cwd string
	// Expanded is true when the tool block is effectively expanded (Full view):
	// either the user expanded this block or the global view mode is "full".
	Expanded bool
	// IsPartial is true while the tool is still streaming or running.
	IsPartial bool
	// IsError is true when the tool returned an error.
	IsError bool
	// ArgsComplete is true when all arguments have been received.
	ArgsComplete bool
	// Args holds the parsed tool arguments, when available. During streaming
	// it may contain partial/extracted fields (e.g. content for write/edit).
	Args map[string]any
	// PreviewLines is the configured number of input/output lines to show
	// when Expanded is false (Summary view). It is the single source of truth
	// for the collapsed line count across all tools (config tui.tools.preview_lines).
	PreviewLines int
}
