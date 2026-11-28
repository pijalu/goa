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
	// CapturesInput marks an overlay that owns the keyboard while visible
	// (selectors, confirms). The web block plane falls back to full-cell
	// frames while one is up; overlays without it (the autocomplete popup)
	// merely extend the rendered band.
	CapturesInput bool
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

	// ScrollTop is the Compositor's scrollback watermark at build time. compose
	// clamps the window top to it exactly like the paint path does
	// (Compositor.windowTop), so overlay placement and row painting share one
	// geometry: an overlay placed against the unclamped bottom-anchored window
	// top lands on canvas rows the painter then treats as chrome/blank when a
	// shrink has lifted the clamp — splitting a popup across both phases and
	// leaving stale rows plus blank gaps on screen.
	ScrollTop int

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

	// Blocks is the conversation as semantic blocks (specs/webui.md §22): one
	// per transcript entry plus the header art. Built by buildScene from the
	// components' own Model state; the compositor ignores it. The web block
	// plane diffs it into id-keyed upserts, so the page renders HTML flow
	// content instead of re-deriving semantics from cell rows.
	Blocks []SceneBlock
	// BlockWidth is the width the header art Lines were rendered at, so a
	// consumer knows when they must be re-shipped.
	BlockWidth int
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
// padToScreen pads a canvas shorter than the terminal with blank TRANSCRIPT
// rows, inserted immediately before the pinned chrome band, so the canvas spans
// the whole screen and no screen row is left as unpaintable filler. It is a
// no-op when nothing needs filling: the canvas already fills the screen, the
// terminal geometry is unknown, or rows have already been emitted into
// scrollback (ScrollTop > 0), where inserting blank rows would shift the row
// identities the watermark refers to.
func (s *Scene) padToScreen(canvas []string) []string {
	paddedHeight := s.screenPaddedHeight(len(canvas))
	if paddedHeight == len(canvas) {
		return canvas
	}
	termH := s.TerminalH
	chromeH := max(0, s.ChromeHeight)
	chromeH = min(chromeH, termH)
	contentEnd := max(0, len(canvas)-chromeH)
	pad := paddedHeight - len(canvas)
	padded := make([]string, 0, termH)
	padded = append(padded, canvas[:contentEnd]...)
	padded = append(padded, make([]string, pad)...)
	padded = append(padded, canvas[contentEnd:]...)
	return padded
}

// screenPaddedHeight returns the canvas height after the overlay-only screen
// padding, without allocating the canvas. Cursor extraction uses it to derive
// exactly the same window mapping as compose.
func (s *Scene) screenPaddedHeight(height int) int {
	if s.TerminalH < 1 || s.ScrollTop > 0 || height >= s.TerminalH {
		return height
	}
	return s.TerminalH
}

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
	// Share the painter's window top: Compositor.windowTop clamps the viewport
	// to the scrollback watermark so rows already committed to terminal
	// scrollback are never repainted. Overlay placement must use the SAME top,
	// otherwise an overlay is written to canvas rows the painter maps to the
	// pinned chrome band (or to blank filler), which splits it in two and
	// leaves stale/blank rows on screen.
	if viewportStart < s.ScrollTop {
		viewportStart = s.ScrollTop
	}
	m := s.mapping(height)
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
	// An overlay floats over the SCREEN, so it needs canvas rows to sit on for
	// every screen row it may use: pad a short canvas up to the terminal height
	// and re-derive the geometry for the padded canvas. Without this the window
	// mapping leaves blank filler screen rows that no canvas row maps to (the
	// painter blanks them every frame), so an overlay over them — e.g. a
	// full-screen selector on an otherwise empty scene — was truncated away.
	// Padding is only safe while nothing scrolled (see padToScreen) and only
	// happens for overlay frames, so base canvases keep their exact content
	// length.
	if padded := s.padToScreen(canvas); len(padded) != len(canvas) {
		canvas = padded
		height = len(canvas)
		m = s.mapping(height)
		visibleEnd = min(height, max(viewportStart, m.top)+s.TerminalH)
	}
	canvas = placeOverlays(canvas, overlays, m, s.TerminalW)
	return applyLineResets(canvas, placeStart, visibleEnd), true
}

// windowMapping is the ONE canvas-row ↔ screen-row geometry of a frame. The
// painter (drawWindow), the agent view (Scene.AgentFrame) and overlay
// placement all derive their rows from it, so they can never disagree.
//
// The mapping is piecewise because the visible window is assembled from two
// independent anchors:
//
//   - the TRANSCRIPT phase draws canvas rows [top, transcriptEnd) top-down from
//     screen row 1, where top is the bottom-anchored viewport top clamped to the
//     scrollback watermark (rows below the watermark are already committed and
//     must never be repainted).
//   - the CHROME band (status line, goal bubble, editor, footer) is PINNED to
//     the bottom: its first canvas row (contentEnd) is always painted at screen
//     row windowH+1. A watermark clamp that shortens the transcript phase
//     therefore leaves blank filler rows between the two phases instead of
//     sliding the chrome up.
//
// Overlays are placed relative to screen rows (see placeOverlays) and must stay
// inside the transcript phase: a row of an overlay that leaked into the chrome
// phase would be painted at a different screen row than intended, splitting it
// and leaving stale rows plus blank gaps on screen.
type windowMapping struct {
	top           int // canvas row painted on screen row 1 (watermark-clamped)
	transcriptEnd int // canvas row where the transcript phase stops (exclusive)
	contentEnd    int // first canvas row of the pinned chrome band
	windowH       int // transcript region height in screen rows
	canvasLen     int
}

// newWindowMapping computes the frame geometry from the canvas length, the
// terminal height, the pinned chrome height and the scrollback watermark.
// scrollTop == 0 means "no watermark yet" (scrollback reset paths).
func newWindowMapping(canvasLen, termH, chromeH, scrollTop int) windowMapping {
	if termH < 1 {
		termH = 24
	}
	if chromeH < 0 {
		chromeH = 0
	}
	if chromeH > termH {
		chromeH = termH
	}
	windowH := termH - chromeH
	if windowH < 1 {
		windowH = 1
	}
	contentEnd := canvasLen - chromeH
	if contentEnd < 0 {
		contentEnd = 0
	}
	top := max(0, canvasLen-termH)
	if top < scrollTop {
		top = scrollTop
	}
	transcriptEnd := min(top+windowH, contentEnd)
	return windowMapping{
		top:           top,
		transcriptEnd: transcriptEnd,
		contentEnd:    contentEnd,
		windowH:       windowH,
		canvasLen:     canvasLen,
	}
}

// mapping returns this scene's frame geometry for a canvas of canvasLen rows.
func (s *Scene) mapping(canvasLen int) windowMapping {
	return newWindowMapping(canvasLen, s.TerminalH, s.ChromeHeight, s.ScrollTop)
}

// canvasRow maps a 0-based screen row to the canvas row painted there. ok is
// false for the blank filler rows left when the watermark clamp shortens the
// transcript phase (chrome stays pinned to the screen bottom).
func (m windowMapping) canvasRow(screenRow int) (int, bool) {
	if screenRow < 0 {
		return 0, false
	}
	if screenRow < m.windowH {
		c := m.top + screenRow
		if c < m.transcriptEnd {
			return c, true
		}
		return 0, false
	}
	c := m.contentEnd + (screenRow - m.windowH)
	if c < m.canvasLen {
		return c, true
	}
	return 0, false
}

// transcriptRows is the number of screen rows the transcript phase paints —
// the screen rows an overlay may occupy.
func (m windowMapping) transcriptRows() int {
	return max(0, m.transcriptEnd-m.top)
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

// placeOverlays composites overlay layers onto the canvas and returns the
// updated canvas. Overlay Rect.Y is SCREEN-relative (0 = top of the visible
// viewport); it is converted to a canvas row through the frame's
// windowMapping, and clamped so the overlay stays inside the transcript phase
// — the only screen rows the transcript paint phase draws from. An overlay that
// leaked past the transcript phase would be painted by the chrome phase at a
// different screen row (the two anchors diverge whenever the watermark clamp
// lifts the window top), leaving stale rows and blank gaps on screen.
//
// An overlay taller than the transcript phase is truncated to the rows that can
// be shown.
func placeOverlays(canvas []string, overlays []Layer, m windowMapping, termW int) []string {
	for _, l := range overlays {
		placed := l
		placed.Rect = overlayCanvasRect(l.Rect, len(l.Content), m)
		placed.Content = l.Content[:placed.Rect.H]
		for len(canvas) < placed.Rect.Y+placed.Rect.H {
			canvas = append(canvas, "")
		}
		placeLayer(canvas, placed, termW, m.top, m.top+m.windowH)
	}
	return canvas
}

// overlayCanvasRect applies the overlay's clipping and screen-to-canvas
// translation. Both painting and cursor extraction go through this function so
// a selector clamped above pinned chrome cannot leave its cursor at the
// unclamped screen row.
func overlayCanvasRect(rect Rect, contentLen int, m windowMapping) Rect {
	transcriptRows := max(1, m.transcriptRows())
	h := min(rect.H, contentLen)
	if h > transcriptRows {
		h = transcriptRows
	}
	y := rect.Y
	if y+h > transcriptRows {
		y = transcriptRows - h
	}
	y = max(0, y)
	return Rect{X: rect.X, Y: m.top + y, W: rect.W, H: h}
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
