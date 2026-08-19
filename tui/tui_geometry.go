// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
)

func extractCursorMarker(scene *Scene) {
	baseHeight := baseCanvasHeight(scene.Layers)
	termH := scene.TerminalH
	if termH < 1 {
		termH = 24
	}
	viewportStart := max(0, baseHeight-termH)

	if row, col, found := findCursorInLayers(scene.Layers, LayerOverlay, viewportStart); found {
		scene.Cursor = &CursorPos{Row: row, Col: col}
		return
	}
	if scene.OverlayCapturesInput {
		// Capturing overlay is open and has no cursor of its own: hide the
		// cursor so it does not leak through from the underlying editor.
		return
	}
	if row, col, found := findCursorInLayers(scene.Layers, LayerBase, 0); found {
		scene.Cursor = &CursorPos{Row: row, Col: col}
	}
}

// findCursorInLayers scans layers of the given kind from top to bottom and
// returns the cursor position if a CURSOR_MARKER is found. The yOffset is added
// to the row coordinate for overlay layers.
func findCursorInLayers(layers []Layer, kind LayerKind, yOffset int) (int, int, bool) {
	for li := len(layers) - 1; li >= 0; li-- {
		l := &layers[li]
		if l.Kind != kind {
			continue
		}
		rowOffset := yOffset + l.Rect.Y
		if row, col, found := findCursorInLayer(l, rowOffset); found {
			return row, col, true
		}
	}
	return 0, 0, false
}

func findCursorInLayer(l *Layer, rowOffset int) (int, int, bool) {
	for ri := len(l.Content) - 1; ri >= 0; ri-- {
		line := l.Content[ri]
		idx := strings.Index(line, CURSOR_MARKER)
		if idx < 0 {
			continue
		}
		before := line[:idx]
		col := visibleWidth(before)
		l.Content[ri] = before + line[idx+len(CURSOR_MARKER):]
		return rowOffset + ri, col, true
	}
	return 0, 0, false
}

// componentLayerName returns a short semantic name for a component, used to
// label layers in the AgentView so AI tooling can identify screen regions.
func componentLayerName(c Component) string {
	name := fmt.Sprintf("%T", c)
	name = strings.TrimPrefix(name, "*")
	name = strings.TrimPrefix(name, "tui.")
	return name
}

// clampOverlayHeight clamps an overlay's requested height to the terminal.
func clampOverlayHeight(requested, termH int) int {
	if requested > termH {
		return termH
	}
	if requested < 1 {
		return 1
	}
	return requested
}

// overlayStartRow computes the viewport-relative top row for an overlay.
func overlayStartRow(opts OverlayOptions, height, termH int) int {
	var startRow int
	if opts.Center {
		startRow = (termH - height) / 2
	} else {
		startRow = termH - height - opts.BottomOffset
	}
	if startRow < 0 {
		return 0
	}
	return startRow
}
func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// max returns the larger of two integers.
func max(a, b int) int {
	if a > b {
		return a
	}
	return b
}
