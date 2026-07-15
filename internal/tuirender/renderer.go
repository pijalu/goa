// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package tuirender defines the interface between tool implementations and the
// TUI renderer. It lives in internal/tuirender so tool packages can implement
// renderers without creating an import cycle with the tui package.
package tuirender

import "strings"

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
	// Stats holds computed line/byte counts for the current input args and
	// output, so renderers and the widget can show a uniform live counter.
	Stats StreamStats
}

// StreamStats summarizes the size of a tool call's streamed input (args) and
// output. It is computed generically so every tool gets the same live counter
// without each renderer re-deriving it.
type StreamStats struct {
	InputLines  int
	InputBytes  int
	OutputLines int
	OutputBytes int
	HasInput    bool
	HasOutput   bool
}

// StatsFor computes a StreamStats from parsed args (input side) and the raw
// output. The input body is taken as the longest string-valued argument
// (content/command/code/new_string/...), a robust heuristic across tools that
// keeps this package free of tool-specific knowledge.
func StatsFor(args map[string]any, output string) StreamStats {
	s := StreamStats{}
	if body, ok := longestStringArg(args); ok {
		s.HasInput = true
		s.InputBytes = len(body)
		s.InputLines = countLines(body)
	}
	if output != "" {
		s.HasOutput = true
		s.OutputBytes = len(output)
		s.OutputLines = countLines(output)
	}
	return s
}

// longestStringArg returns the longest string-typed value in args (by byte
// length). It is the heuristic "body" of a tool call (file content, command,
// code, …). Boolean/numeric args are ignored.
func longestStringArg(args map[string]any) (string, bool) {
	var best string
	var found bool
	for _, v := range args {
		str, ok := v.(string)
		if !ok || str == "" {
			continue
		}
		if !found || len(str) > len(best) {
			best = str
			found = true
		}
	}
	return best, found
}

// countLines returns the number of lines in s, counting a trailing newline as
// an explicit empty line only when present (so "a\n" → 2, "a" → 1).
func countLines(s string) int {
	if s == "" {
		return 0
	}
	n := strings.Count(s, "\n") + 1
	return n
}
