// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"github.com/pijalu/goa/internal/ansi"
	"github.com/pijalu/goa/internal/tuirender"
)

// ToolRenderer is re-exported from internal/tuirender for tui callers.
type ToolRenderer = tuirender.ToolRenderer

// RenderContext is re-exported from internal/tuirender.
type RenderContext = tuirender.RenderContext

// ToolRendererRegistry maps tool names to their renderers.
var ToolRendererRegistry = map[string]ToolRenderer{}

// RegisterToolRenderer registers a renderer for a tool name. Later
// registrations overwrite earlier ones.
func RegisterToolRenderer(name string, r ToolRenderer) {
	ToolRendererRegistry[name] = r
}

// GetToolRenderer returns the renderer for a tool name, or nil if none is
// registered.
func GetToolRenderer(name string) ToolRenderer {
	return ToolRendererRegistry[name]
}

// genericRenderer is the fallback renderer for tools without a dedicated
// renderer. Fallback: bold tool name + dim args.
type genericRenderer struct{}

func (genericRenderer) RenderCall(args map[string]any, ctx RenderContext) string {
	return ansiBoldToolTitle("run tool")
}

func (genericRenderer) RenderResult(output string, ctx RenderContext) string {
	if output == "" {
		return ""
	}
	return output
}

func (genericRenderer) PreviewLines() int             { return 20 }
func (genericRenderer) HideResultWhenCollapsed() bool { return false }

func ansiBoldToolTitle(text string) string {
	return ansi.Bold + ansi.Fg(TheTheme.ColorHex("toolTitle")) + text + ansi.BoldReset + ansi.FgReset
}

func ansiToolOutput(text string) string {
	return ansi.Fg(TheTheme.ColorHex("toolOutput")) + text + ansi.FgReset
}

func ansiMuted(text string) string {
	return ansi.Fg(TheTheme.ColorHex("system_msg")) + text + ansi.FgReset
}
