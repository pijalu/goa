// SPDX-License-Identifier: GPL-3.0-or-later

package tui

type Rect struct{ X, Y, W, H int }

// LayerKind classifies how the Compositor treats a layer.
type LayerKind int

const (
	// LayerBase is stacked content that participates in scrollback scrolling.
	LayerBase LayerKind = iota
	// LayerOverlay is transient and composited on top of the visible viewport.
	LayerOverlay
)

// Layer is one piece of the screen: a named, positioned, z-ordered block of
// styled content. It is protocol-free (no cursor-positioning escapes; SGR
// styling is allowed because it is content, not protocol). The Compositor
// places it on the canvas; the AgentView reads it directly.
type Layer struct {
	Name    string
	Kind    LayerKind
	Z       int      // higher draws on top of lower
	Rect    Rect     // position/size in the virtual buffer
	Content []string // styled lines; expected len == Rect.H
}

// CursorPos is a logical cursor position in virtual-buffer coordinates.
type CursorPos struct{ Row, Col int }

// Scene is the complete protocol-free description of one frame: the terminal
// size, the layers, the input cursor, and a DOM of component nodes. It is the
// single source of truth consumed by the Compositor (terminal bytes), the
// AgentView (plain text for AI tooling), and tests.
type Scene struct {
	TerminalW            int
	TerminalH            int
	OverlayCapturesInput bool       // true when at least one overlay has CaptureInput
	Layers               []Layer    // base layers first, then overlays; ordering within equal Z is stable
	Cursor               *CursorPos // nil hides the hardware cursor
	Nodes                []AgentNode
	// ChromeHeight is the number of fixed bottom-chrome rows (status bar, input
	// editor, footer, non-conversational bubbles). They occupy the LAST
	// ChromeHeight rows of the composed canvas and are never emitted into
	// scrollback: the scrollback watermark (Compositor.scrollTop) is clamped to
	// the start of the chrome band, so chrome can never scroll off the top.
	// 0 = no pinned chrome (the whole canvas is scrollable transcript).
	ChromeHeight int

	// WidthChanged reports that the terminal width differs from the previous
	// frame. The Compositor sets it before calling compose; on a width change
	// compose must materialize the FULL canvas (not just the visible window),
	// because the scrollback reset re-emits every off-screen row from it.
	WidthChanged bool

	// ClearGen is the Compositor's clear generation at snapshot time. The
	// snapshot's owner (TUI.buildSnapshot) stamps it from Compositor.ClearGen;
	// Render DROPS scenes whose generation is older than the compositor's
	// current one (they predate a Clear and would repaint stale content,
	// swallowing the pending wipe — the /new blank-screen race).
	ClearGen uint64

	// MutationGen is the TUI's mutation generation at snapshot time (bumped by
	// every command). The compositor compares it across frames to detect when
	// the conversation has settled (no mutation since the last frame) so it can
	// re-sync a deferred scrollback exactly once instead of on every stream
	// chunk.
	MutationGen uint64
}

// compose builds the virtual-buffer canvas from the Scene's base layers, each
// placed at its Rect, and reports whether any overlay layer is present.
//
// cullFloor is the canvas row below which content may be left unwritten for
// efficiency: rows below it are already in the terminal's scrollback and are
// not read this frame. The steady-state scroll emission reads rows at/above
// the scrollback watermark (scrollTop), so that watermark is the correct cull
// boundary — rows below it are provably already scrolled, while culling at or
// above it would drop rows the emission still needs and re-emit them as blanks
// (the lost-content corruption). Pass 0 to materialize the full canvas, which
// reset frames (width or bottom-chrome height change) require because they
// re-emit the entire off-screen transcript.
//
// This is the single place that decides pixel placement of base content.
// Overlays are composited separately (viewport-relative) by the caller's
// render path, never here.
func (s *Scene) compose(cullFloor int) (canvas []string, hasOverlay bool) {
	height := baseCanvasHeight(s.Layers)
	if height == 0 {
		height = 1
	}
	canvas = make([]string, height)

	viewportStart := max(0, height-s.TerminalH)
	visibleEnd := min(height, viewportStart+s.TerminalH)
	if visibleEnd <= viewportStart {
		visibleEnd = viewportStart + 1
	}
	placeStart := cullFloor
	if placeStart < 0 {
		placeStart = 0
	}
	// Never cull the visible window itself: even if the floor is ahead of the
	// viewport top (a transient watermark overshoot), the window must be
	// materialized so it can be repainted.
	if placeStart > viewportStart {
		placeStart = viewportStart
	}
	// On a WIDTH change the compositor wipes the terminal scrollback (\x1b[3J)
	// and re-emits the whole off-screen transcript from this canvas, so every
	// row must be materialized regardless of the floor.
	if s.WidthChanged {
		placeStart = 0
	}

	for _, l := range s.Layers {
		if l.Kind == LayerBase {
			placeLayer(canvas, l, s.TerminalW, placeStart, visibleEnd)
		}
	}

	overlays := overlaysOf(s.Layers)
	if len(overlays) == 0 {
		return applyLineResets(canvas, placeStart, visibleEnd), false
	}
	canvas = placeOverlays(canvas, overlays, height, s.TerminalH, s.TerminalW)
	return applyLineResets(canvas, placeStart, visibleEnd), true
}

// baseCanvasHeight returns the canvas height needed for base (non-overlay)
// layers: the max bottom (Y+H) over them.
func baseCanvasHeight(layers []Layer) int {
	height := 0
	for _, l := range layers {
		if l.Kind == LayerOverlay {
			continue
		}
		if bottom := l.Rect.Y + l.Rect.H; bottom > height {
			height = bottom
		}
	}
	return height
}

// overlaysOf collects overlay layers in stable Z order.
func overlaysOf(layers []Layer) []Layer {
	var overlays []Layer
	for _, l := range layers {
		if l.Kind == LayerOverlay {
			overlays = append(overlays, l)
		}
	}
	sortLayersByZ(overlays)
	return overlays
}

// placeOverlays composites overlay layers (viewport-relative Y) onto the
// canvas, extending it as needed, and returns the updated canvas.
func placeOverlays(canvas []string, overlays []Layer, baseHeight, termH, termW int) []string {
	viewportStart := baseHeight - termH
	if viewportStart < 0 {
		viewportStart = 0
	}
	for _, l := range overlays {
		absY := viewportStart + l.Rect.Y
		for len(canvas) < absY+l.Rect.H {
			canvas = append(canvas, "")
		}
		placed := l
		placed.Rect = Rect{X: l.Rect.X, Y: absY, W: l.Rect.W, H: l.Rect.H}
		placeLayer(canvas, placed, termW, viewportStart, viewportStart+termH)
	}
	return canvas
}

// placeLayer writes a layer's Content onto the canvas at its Rect, padding
// each content line to the layer's width and truncating overwidth lines.
// Lines outside the visible region [viewportStart, visibleEnd) are skipped.
//
// Rather than iterating every content line and bounds-checking, it computes
// the content-index subrange that maps into the visible canvas rows and
// iterates only that. This keeps placeLayer O(visible) even when a layer's
// Content is the full conversation transcript (the chat layer), so streaming
// frames do not pay O(history) per layer.
func placeLayer(canvas []string, l Layer, termW, viewportStart, visibleEnd int) {
	if len(l.Content) == 0 {
		return
	}
	// y = l.Rect.Y + i must satisfy viewportStart <= y < visibleEnd.
	start := viewportStart - l.Rect.Y
	end := visibleEnd - l.Rect.Y
	if start < 0 {
		start = 0
	}
	if end > len(l.Content) {
		end = len(l.Content)
	}
	if start >= end {
		return
	}
	for i := start; i < end; i++ {
		y := l.Rect.Y + i
		if y < 0 || y >= len(canvas) {
			continue
		}
		line := l.Content[i]
		if vw := visibleWidth(line); vw > termW {
			line = truncateToWidth(line, termW, "")
		}
		canvas[y] = line
	}
}

func sortLayersByZ(layers []Layer) {
	// Stable insertion sort by Z (small N; keeps equal-order stable).
	for i := 1; i < len(layers); i++ {
		for j := i; j > 0 && layers[j-1].Z > layers[j].Z; j-- {
			layers[j-1], layers[j] = layers[j], layers[j-1]
		}
	}
}

// AgentLayer is the ANSI-free, structured view of one layer for AI tooling.
