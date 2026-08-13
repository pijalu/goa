// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"sync"

	"github.com/pijalu/goa/internal/ansi"
)

// Rect is a logical rectangle in the virtual buffer (0-indexed).
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
type AgentLayer struct {
	Name    string
	Z       int
	Rect    Rect
	Lines   []string // ANSI-stripped content
	Visible bool     // whether any part falls inside the visible viewport
}

// AgentNode represents a single UI element in the agent-accessible DOM. It
// gives a component's screen bounds, type, content, and focus state so tests
// and agents can reason about the TUI without parsing escape sequences.
type AgentNode struct {
	Name     string
	Type     string
	Rect     Rect
	Text     string // ANSI-stripped, newline-separated content
	Focused  bool
	Cursor   *CursorPos // cursor position relative to this node, or nil
	Children []AgentNode
}

// AgentFrame is a structured, protocol-free representation of the current
// screen for AI agent tooling: it lets an agent "see" the TUI without parsing
// escape codes. Computed from the same Scene the Compositor renders, so agent
// and terminal always agree.
type AgentFrame struct {
	Width, Height int
	Cursor        *CursorPos
	Layers        []AgentLayer // in z-order
	Nodes         []AgentNode  // DOM nodes for agentic testing
	Visible       []string     // ANSI-stripped visible viewport, top-to-bottom reading order
}

// AgentFrame produces the plain-text structured view of the Scene.
// viewportH is the terminal height (number of visible rows).
func (s *Scene) AgentFrame(viewportH int) AgentFrame {
	canvas, _ := s.compose(0)
	height := len(canvas)
	vTop := height - viewportH
	if vTop < 0 {
		vTop = 0
	}
	vBottom := vTop + viewportH
	if vBottom > height {
		vBottom = height
	}

	frame := AgentFrame{Width: s.TerminalW, Height: viewportH, Cursor: s.Cursor, Nodes: s.Nodes}
	frame.Nodes = fillNodeText(frame.Nodes, s.Layers)

	// Layers, base then overlays by Z, all ANSI-stripped.
	ordered := make([]Layer, 0, len(s.Layers))
	ordered = append(ordered, s.Layers...)
	sortLayersByZ(ordered)
	for _, l := range ordered {
		al := AgentLayer{Name: l.Name, Z: l.Z, Rect: l.Rect}
		al.Lines = make([]string, len(l.Content))
		for i, line := range l.Content {
			al.Lines[i] = ansi.Strip(line)
		}
		al.Visible = l.Rect.Y < vBottom && l.Rect.Y+l.Rect.H > vTop
		frame.Layers = append(frame.Layers, al)
	}

	// Visible viewport, top-to-bottom.
	for i := vTop; i < vBottom && i < len(canvas); i++ {
		frame.Visible = append(frame.Visible, ansi.Strip(canvas[i]))
	}
	return frame
}

// Compositor owns ALL terminal-protocol concerns: it composes a Scene's
// layers into a virtual canvas, then renders that canvas to the terminal.
//
// # Rendering model
//
// The compositor maintains three exact quantities per frame, and the terminal
// is driven purely as an output device for them:
//
//	V        — the full virtual canvas (transcript + fixed chrome band).
//	scrollTop — the scrollback watermark: rows V[0:scrollTop] have been emitted
//	           into the terminal's scrollback EXACTLY once, in order, and are
//	           never re-emitted. scrollTop is clamped to the chrome band start
//	           so fixed chrome can never scroll off the top.
//	vt       — the viewport top: rows V[vt : vt+height] are the visible window,
//	           drawn each frame with absolute CUP. vt = max(0, len(V)-height).
//
// A frame is therefore one atomic CSI-2026 sync containing:
//  1. the newly scrolled-off rows V[prevScrollTop : scrollTop], each written
//     followed by \n so the terminal pushes them into scrollback in order;
//  2. the visible window, repainted with absolute CUP (skipped for rows whose
//     bytes are unchanged since the previous frame);
//  3. the hardware-cursor restore, folded into the same sync.
//
// There is exactly ONE scroll path — no first-scroll / large-scroll /
// shrink / delete special cases. Because scrollback rows are written
// explicitly and monotonically (scrollTop never decreases except on an
// explicit resize/clear, which resets state), correctness does not depend on
// the terminal's incidental native-scroll side effects.
//
// The diff math is kept cohesive with the render logic itself.
type Compositor struct {
	terminal Terminal

	mu sync.Mutex // serializes Render/Restore/Buffer against each other

	// prevLines is the previous frame's full visible-window baseline used for
	// the unchanged-row skip. Index i is the canvas row of the PREVIOUS frame.
	prevLines []string
	prevW     int
	prevH     int

	// scrollTop is the scrollback watermark described above.
	scrollTop int
	// vt is the previous frame's viewport top (first visible canvas row).
	// It is clamped to scrollTop by windowTop: the window never starts above
	// the watermark, so a row already emitted into terminal scrollback is
	// never repainted onto the visible screen.
	vt int
	// cursorRow is the canvas row the hardware cursor was left on.
	cursorRow         int
	hardwareCursorRow int

	fullRedrawCount int

	// cursorVisible tracks the terminal's cursor-show state so we only emit
	// \x1b[?25h / \x1b[?25l on a real transition, never as a redundant per-frame
	// write. It is updated solely inside the synced frame buffers.
	cursorVisible bool

	// chromeH is the fixed bottom-chrome band height for the current frame
	// (Scene.ChromeHeight). scrollTop is clamped so it never enters the band.
	chromeH int
	// prevChromeH is the chrome band height of the PREVIOUS frame — the value
	// that maps prevLines's content end. prevWindowFull consults it so the
	// steady-scroll geometry check stays correct across a chrome height
	// change instead of misreading the previous canvas by the chrome delta.
	prevChromeH int
	// regionBot is the DECSTBM scroll-region bottom currently in effect on the
	// terminal (1-indexed; region top is always row 1), or 0 when no region is
	// set (full-screen scroll). When chromeH > 0 the compositor confines the
	// line-feed scroll to the transcript region [1, height-chromeH] so that
	// emitting scrollback rows never moves the pinned chrome below the region.
	regionBot int

	// clearRequested is set by Clear(): the NEXT Render wipes the screen and
	// scrollback atomically inside its own CSI-2026 sync, then repaints the
	// fresh canvas. Deferring the wipe into the frame (instead of writing it
	// immediately) removes the atomicity gap where a stale pre-clear frame
	// could be painted after the wipe — the /new blank-screen race.
	clearRequested bool

	// lastScrollCount is the number of rows the current frame's scroll advance
	// (emitSteadyScroll) wrote at the bottom of the transcript region. It is set
	// immediately before repaintWindow runs and consumed only by it, then reset
	// to 0 for the next frame. repaintWindow skips those already-written bottom
	// rows so a scrolling frame never emits a row twice.
	lastScrollCount int

	// tracer, when non-nil, records one JSONL frame per Render for offline
	// diagnosis of byte-level rendering bugs. curTrace is the in-progress
	// record for the current Render, owned by the lock holder; nil when
	// tracing is disabled.
	tracer   *renderTracer
	curTrace *frameTrace
}

// NewCompositor creates a Compositor bound to a Terminal. cursorVisible starts
// false: TUI.Start hides the hardware cursor before the first frame, so the
// first cursor-bearing frame must emit the show-cursor transition (\x1b[?25h).
func NewCompositor(term Terminal) *Compositor {
	return &Compositor{terminal: term, cursorVisible: false}
}

// EnableRenderTrace turns on per-frame JSONL tracing to the given path.
func (c *Compositor) EnableRenderTrace(path string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	tr, err := newRenderTracer(path)
	if err != nil {
		return err
	}
	c.tracer = tr
	return nil
}

// FullRedrawCount reports how many frames took the full-repaint path.
func (c *Compositor) FullRedrawCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fullRedrawCount
}

func (c *Compositor) beginTrace(scene *Scene, canvas []string, w, h int) {
	if c.tracer == nil {
		return
	}
	ft := &frameTrace{TermW: w, TermH: h, CanvasLen: len(canvas)}
	for _, l := range scene.Layers {
		ft.Layers = append(ft.Layers, layerTrace{
			Name: l.Name, Kind: int(l.Kind), Z: l.Z,
			Y: l.Rect.Y, H: l.Rect.H, W: l.Rect.W, ContentLen: len(l.Content),
		})
	}
	c.curTrace = ft
}

func (c *Compositor) emitTrace() {
	if c.tracer == nil || c.curTrace == nil {
		c.curTrace = nil
		return
	}
	c.tracer.emit(*c.curTrace)
	c.curTrace = nil
}

func (c *Compositor) setTracePath(path string) {
	if c.curTrace != nil {
		c.curTrace.Path = path
	}
}

func (c *Compositor) traceWroteRow(row int) {
	if c.curTrace != nil {
		c.curTrace.WroteRows = append(c.curTrace.WroteRows, row)
	}
}

// PrevSize reports the last-rendered terminal size (width, height).
func (c *Compositor) PrevSize() (int, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.prevW, c.prevH
}

// Buffer returns a copy of the previous frame's composed canvas.
func (c *Compositor) Buffer() []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return copySlice(c.prevLines)
}

// InitialClear wipes the terminal before the first frame.
func (c *Compositor) InitialClear() {
	c.terminal.Write([]byte("\x1b[?2026h\x1b[2J\x1b[H\x1b[3J\x1b[?2026l"))
}

// Clear resets the compositor for a deliberate content reset (e.g. /new, a
// session switch): it wipes the screen AND terminal scrollback and zeroes the
// scrollback watermark, so the next Render treats the new (typically short)
// canvas as a fresh first frame rather than a transient collapse.
//
// This is distinct from the steady-state watermark clamp (windowTop): a clamp
// handles a *transient* mid-transcript shrink by anchoring the window at
// scrollTop, whereas Clear handles an *intentional* reset where the old
// transcript is gone for good and its scrollback must not linger. Without an
// explicit Clear, a new session on a scrolled terminal would either flash the
// old header (the pre-fix bug) or sit on a blank window anchored at a stale
// watermark (the clamp) — both wrong.
func (c *Compositor) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	// Mark state cleared; the next Render performs the actual wipe atomically
	// with its repaint (see clearRequested). No terminal write happens here, so
	// a concurrent in-flight frame cannot interleave with the wipe.
	c.scrollTop = 0
	c.vt = 0
	c.prevLines = nil
	c.regionBot = 0
	c.clearRequested = true
}

// Restore is called on shutdown: end synchronized output, reset SGR, move the
// cursor below content, and show it so the terminal is usable after exit.
func (c *Compositor) Restore() {
	c.mu.Lock()
	defer c.mu.Unlock()
	var buf strings.Builder
	buf.WriteString("\x1b[?2026l")
	buf.WriteString("\x1b[0m")
	buf.WriteString("\x1b[r") // reset scroll region so the shell scrolls normally
	c.regionBot = 0
	bottom := c.vt + c.prevH
	if bottom <= 0 {
		bottom = len(c.prevLines)
	}
	if bottom > 0 {
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H", bottom))
	}
	buf.WriteString("\r\n")
	c.terminal.Write([]byte(buf.String()))
	c.terminal.ShowCursor()
	if c.tracer != nil {
		c.tracer.close()
		c.tracer = nil
	}
}

// Render composes the Scene's layers into a canvas and renders it: emit the
// newly scrolled-off rows into scrollback, repaint the visible window, restore
// the cursor — all in one synchronized frame.
func (c *Compositor) Render(scene *Scene) {
	width, height := scene.TerminalW, scene.TerminalH
	if width < 20 {
		width = 80
	}
	if height < 10 {
		height = 24
	}
	scene.TerminalW = width
	scene.TerminalH = height

	c.mu.Lock()
	defer c.mu.Unlock()

	// A pending Clear() must be honored by THIS frame: wipe the screen and
	// scrollback atomically inside the frame's sync, then repaint the fresh
	// canvas. prevLines is already nil (Clear reset it), so classifyFrame
	// returns frameFirst below and drawWindow repaints every row.
	clearPending := c.clearRequested
	c.clearRequested = false

	kind := c.classifyFrame(scene, width, height)
	canvas, _ := scene.compose(kind.cullFloor(c.scrollTop))
	c.beginTrace(scene, canvas, width, height)
	defer c.emitTrace()
	if clearPending {
		c.setTracePath("clear")
	}

	// Mid-transcript edit guard for the incremental paths: when content above
	// the new window top changed identity since the previous frame (a
	// streaming block growing ABOVE later-appended content, e.g. /quota
	// landing mid-stream), the incremental scroll emission would scroll wrong
	// row identities into scrollback and skip the real ones. Rebuild
	// scrollback from the FULL canvas (cullFloor 0 — the steady canvas has
	// rows below the watermark culled) instead.
	if kind == frameDiff || kind == frameFullRepaint {
		vt := c.windowTop(len(canvas), height)
		if c.scrollOffUnstable(canvas, c.scrollTarget(vt, len(canvas))) {
			canvas, _ = scene.compose(0)
			c.drawWindowResetScrollback(canvas, scene.Cursor, width, height)
			c.prevLines = copySlice(canvas)
			c.prevW = width
			c.prevH = height
			return
		}
	}

	switch kind {
	case frameFirst:
		// First frame: InitialClear already wiped screen+scrollback; drawWindow
		// emits any off-screen rows into scrollback then draws the window. When
		// this first frame follows a Clear() the wipe has NOT happened yet, so
		// drawWindow performs it atomically inside the frame's sync.
		c.drawWindow(canvas, scene.Cursor, width, height, clearPending)
	case frameGeometryReset:
		// Width change: the terminal's scrollback no longer corresponds to
		// the canvas layout (wrap reflowed), so the incremental diff cannot
		// map it. Reset scrollback and re-emit every off-screen row at the
		// current geometry, then repaint the window.
		c.drawWindowResetScrollback(canvas, scene.Cursor, width, height)
	case frameFullRepaint:
		// Height-only resize or overlay: drawWindow emits scrolled-off rows
		// then repaints the visible window in place (no screen wipe — the
		// per-row repaint already replaces every row; see drawWindow).
		c.drawWindow(canvas, scene.Cursor, width, height, clearPending)
	default: // frameDiff
		// Mid-window shift guard: the diff repaint (repaintWindow →
		// unchangedRow) assumes the physical screen maps to the previous canvas
		// by a PURE scroll (prevIdx = i - vt + c.vt). A tool widget growing or
		// shrinking INSIDE the visible window inserts/deletes rows and shifts
		// every row below it, breaking that mapping: the line-feed scroll moved
		// the OLD layout, and unchangedRow then wrongly skips rows that are
		// actually stale — leaving duplicate and lost rows around the
		// history↔screen boundary (the reported tool-call duplicate bug). The
		// scroll-off region is unaffected (the shift is below it), so scrollback
		// needs no reset — but the window must be repainted in full rather than
		// diffed. Route to drawWindow (repaints every row) for this frame.
		if c.windowContentShifted(canvas, c.windowTop(len(canvas), height), height) {
			c.drawWindow(canvas, scene.Cursor, width, height, clearPending)
		} else {
			c.renderDiff(canvas, scene.Cursor, width, height)
		}
	}

	c.prevLines = copySlice(canvas)
	c.prevW = width
	c.prevH = height
}

// frameKind classifies a frame so Render dispatches on a single value. The
// geometry (terminal size and bottom-chrome height) determines whether the
// terminal's scrollback still maps to the canvas: a geometry change requires
// a scrollback reset, anything else uses the incremental diff.
type frameKind int

const (
	frameDiff          frameKind = iota // steady state: incremental diff
	frameFirst                          // very first frame
	frameGeometryReset                  // width or bottom-chrome height changed
	frameFullRepaint                    // height-only resize or overlay present
)

// classifyFrame computes the frame kind and updates c.chromeH, c.prevChromeH
// and scene.WidthChanged as a side effect (they describe the current and
// previous geometry).
func (c *Compositor) classifyFrame(scene *Scene, width, height int) frameKind {
	// Track the chrome-band height across frames: prevWindowFull consults
	// prevChromeH to map the PREVIOUS canvas's content end ("Slow
	// performance on very large conversations").
	c.prevChromeH = c.chromeH
	c.chromeH = max(scene.ChromeHeight, 0)

	widthChanged := c.prevW != 0 && c.prevW != width
	heightChanged := c.prevH != 0 && c.prevH != height
	scene.WidthChanged = widthChanged

	switch {
	case c.prevLines == nil:
		return frameFirst
	case widthChanged:
		// Only a WIDTH change invalidates the scrollback↔canvas
		// correspondence (line wrap reflows): reset + re-emit. A bottom-chrome
		// height change (editor newline, steering/goal bubble appearing or
		// clearing) does NOT: canvas rows are immutable, the watermark stays
		// valid, and the incremental diff handles the shift at O(viewport)
		// cost. Routing chrome changes through the geometry reset wiped
		// scrollback and re-emitted the ENTIRE transcript per keystroke —
		// the 100%-CPU-for-seconds hang on very large conversations.
		return frameGeometryReset
	case heightChanged || c.hasOverlay(scene):
		return frameFullRepaint
	default:
		return frameDiff
	}
}

// cullFloor returns the canvas row below which content may be left unwritten:
// a geometry reset re-emits the whole transcript (full canvas, 0); steady
// frames may cull rows already in scrollback (the watermark).
func (k frameKind) cullFloor(scrollTop int) int {
	if k == frameGeometryReset {
		return 0
	}
	return scrollTop
}

// hasOverlay reports whether the scene carries any overlay layer.
func (c *Compositor) hasOverlay(scene *Scene) bool {
	for _, l := range scene.Layers {
		if l.Kind == LayerOverlay {
			return true
		}
	}
	return false
}

// emitOverflow emits into scrollback every transcript row that has scrolled
// off the top since the watermark was last advanced, then advances the
// appendOverflow emits into buf every transcript row that has scrolled off the
// top since the watermark was last advanced, then advances the watermark. It is
// the single place the watermark moves, so a row is emitted exactly once and
// the chrome band is never crossed. The bytes are folded into the caller's
// already-open sync so the scroll and the subsequent window repaint commit
// atomically (no intermediate footer-less frame).
func (c *Compositor) appendOverflow(buf *strings.Builder, canvas []string, width, height int) {
	vt := max(0, len(canvas)-height)
	contentEnd := len(canvas) - c.chromeH
	if contentEnd < 0 {
		contentEnd = 0
	}
	target := vt
	if target > contentEnd {
		target = contentEnd
	}
	if target <= c.scrollTop {
		return // nothing new scrolled off; watermark never moves backward
	}
	// Advance from the row currently at the top of the screen (c.vt) to the new
	// top. Rows before c.vt are already in scrollback (watermark c.scrollTop).
	from := c.vt
	if from < c.scrollTop {
		from = c.scrollTop
	}
	c.emitScrollbackAdvance(buf, canvas, from, target, width, height)
	c.scrollTop = target
}

// drawWindow redraws the whole visible window top-down with absolute CUP in
// one synchronized frame. It first emits any newly scrolled-off rows into
// scrollback (via appendOverflow), then repaints every visible row (CUP +
// \x1b[2K + content). It never wipes the screen first: the repaint loop
// already clears and rewrites EVERY row of the window, so a preceding
// full-screen wipe (\x1b[2J, removed 2026-07-21) was redundant — and
// harmful: on real terminals it visibly blanks the screen before the
// rewrite lands (even inside CSI 2026 on several emulators), which produced
// the black flash / mascot-flicker seen on overlay frames during tool calls
// (Mascot/logo redraw, HIGH). In-place row replacement — the same
// strategy renderDiff uses — is flicker-free and leaves no stale content:
// every row is overwritten, and a shorter canvas shifts the window bottom up
// rather than leaving residue.
func (c *Compositor) drawWindow(canvas []string, cursor *CursorPos, width, height int, wipe bool) {
	c.setTracePath("full")
	c.fullRedrawCount++
	vt := c.windowTop(len(canvas), height)

	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	if wipe {
		// A Clear() requested a screen+scrollback wipe. Fold it into THIS frame's
		// sync (instead of a separate immediate write) so it commits atomically
		// with the repaint — a stale pre-clear frame can never be painted on top.
		buf.WriteString("\x1b[2J\x1b[H\x1b[3J")
	}
	c.appendOverflow(&buf, canvas, width, height)
	// drawWindow repaints every row (first frame / full repaint), so the scroll
	// advance did not pre-write any bottom rows for repaintWindow to skip.
	c.lastScrollCount = 0
	// Two-phase repaint: transcript in [1, windowH], chrome in [windowH+1, height].
	// This keeps the chrome band pinned at the screen bottom even when vt is
	// clamped above the natural anchor (windowTop's watermark clamp).
	windowH := height - c.chromeH
	if windowH < 1 {
		windowH = 1
	}
	contentEnd := len(canvas) - c.chromeH
	if contentEnd < 0 {
		contentEnd = 0
	}
	transcriptEnd := vt + windowH
	if transcriptEnd > contentEnd {
		transcriptEnd = contentEnd
	}
	for i := vt; i < transcriptEnd; i++ {
		screenRow := i - vt + 1
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
		c.traceWroteRow(screenRow)
	}
	// Clear any stale transcript rows between the transcript end and the
	// chrome band (a clamped window can leave rows the taller previous frame
	// painted).
	for screenRow := transcriptEnd - vt + 1; screenRow <= windowH; screenRow++ {
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		c.traceWroteRow(screenRow)
	}
	// Draw the pinned chrome band in the rows below the transcript region.
	for i := contentEnd; i < len(canvas); i++ {
		screenRow := windowH + (i - contentEnd) + 1
		if screenRow > height {
			break
		}
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
		c.traceWroteRow(screenRow)
	}
	c.appendCursorSeq(&buf, cursor, len(canvas), width, vt, height)
	buf.WriteString("\x1b[?2026l")
	c.terminal.Write([]byte(buf.String()))

	c.vt = vt
	c.cursorRow = max(0, len(canvas)-1)
	c.hardwareCursorRow = max(0, len(canvas)-1)
}

// drawWindowResetScrollback handles a terminal WIDTH change. At a new width
// the line wrap reflows, so the rows already sitting in the terminal's
// scrollback (emitted at the old width) no longer correspond to the canvas —
// leaving them produces a stale, misaligned history. This clears the
// scrollback (\x1b[3J), resets the watermark, re-emits every off-screen
// transcript row at the new width, then repaints the visible window. The
// result is a scrollback that matches the new layout exactly, as if the app
// had been rendered at this width all along.
func (c *Compositor) drawWindowResetScrollback(canvas []string, cursor *CursorPos, width, height int) {
	c.setTracePath("full-reset")
	c.fullRedrawCount++
	vt := max(0, len(canvas)-height)

	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	// Wipe the visible screen AND the scrollback so no old-width rows survive.
	buf.WriteString("\x1b[2J\x1b[H\x1b[3J")
	// Reset the watermark so every off-screen row is re-emitted at the new
	// width (appendOverflow would otherwise treat them as already-scrolled).
	c.scrollTop = 0
	c.vt = 0
	c.reemitScrollback(&buf, canvas, vt, width, height)
	// Repaint the visible window. The transcript occupies only the top
	// height-chromeH rows (the chrome band is pinned below), so the window is
	// windowH rows tall, not height. Repainting canvas[vt:] (height rows) would
	// overflow chromeH rows into the chrome zone and duplicate content — the
	// reset-path duplicate-row bug. Stop the transcript repaint at windowH,
	// then draw the chrome band in its own rows below.
	windowH := height - c.chromeH
	if windowH < 1 {
		windowH = 1
	}
	transcriptEnd := vt + windowH
	if transcriptEnd > len(canvas)-c.chromeH {
		transcriptEnd = len(canvas) - c.chromeH
	}
	for i := vt; i < transcriptEnd; i++ {
		screenRow := i - vt + 1
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
		c.traceWroteRow(screenRow)
	}
	// Draw the pinned chrome band in the rows below the transcript region.
	for i := transcriptEnd; i < len(canvas); i++ {
		screenRow := windowH + (i - transcriptEnd) + 1
		if screenRow > height {
			break
		}
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
		c.traceWroteRow(screenRow)
	}
	c.appendCursorSeq(&buf, cursor, len(canvas), width, vt, height)
	buf.WriteString("\x1b[?2026l")
	c.terminal.Write([]byte(buf.String()))

	c.scrollTop = vt // everything above the window is now in scrollback
	c.vt = vt
	c.cursorRow = max(0, len(canvas)-1)
	c.hardwareCursorRow = max(0, len(canvas)-1)
}

// reemitScrollback writes every transcript row above the window (rows
// [0, vt)) into scrollback at the current width, top-down, after a scrollback
// reset. It mirrors emitFirstFrameScroll: writing the full transcript and
// letting line-feeds scroll the top rows off leaves exactly [0, vt) in
// scrollback and [vt, vt+windowH) on screen.
func (c *Compositor) reemitScrollback(buf *strings.Builder, canvas []string, vt, width, height int) {
	windowH, _ := c.transcriptWindow(buf, height)
	contentEnd := max(0, len(canvas)-c.chromeH)
	c.emitFirstFrameScroll(buf, canvas, vt, windowH, contentEnd, width)
	if c.chromeH > 0 {
		c.resetScrollRegion(buf)
	}
}

// renderDiff is the steady-state path: emit newly scrolled-off rows, then
// repaint only the changed rows of the visible window.
func (c *Compositor) renderDiff(canvas []string, cursor *CursorPos, width, height int) {
	c.setTracePath("diff")
	// The window top is clamped to the scrollback watermark: when the canvas
	// transiently shrinks below scrollTop (e.g. a thinking block finalized
	// into a shorter entry for one frame), the window stays anchored at the
	// rows the terminal actually shows instead of following vt below the
	// watermark and repainting already-scrolled rows (the mascot flash).
	vt := c.windowTop(len(canvas), height)
	target := c.scrollTarget(vt, len(canvas))

	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	c.advanceScrollback(&buf, canvas, target, width, height)
	// repaintWindow must not redraw the rows the scroll just revealed at the
	// bottom of the window. The count must mirror what advanceScrollback
	// actually wrote — (target - max(c.vt, c.scrollTop)) — NOT (target - c.vt):
	// when the window top moved but the watermark did NOT advance (canvas
	// shrank then regrew, e.g. a stream finalizing mid-flip), the scroll wrote
	// ZERO rows, and counting (target - c.vt) skips a bottom transcript row
	// that was never repainted — leaving a stale row behind (Bug D:
	// a tool widget's last line kept the pending bg after success).
	c.lastScrollCount = max(0, target-max(c.vt, c.scrollTop))
	c.repaintWindow(&buf, canvas, vt, width, height)
	c.appendCursorSeq(&buf, cursor, len(canvas), width, vt, height)
	buf.WriteString("\x1b[?2026l")
	c.terminal.Write([]byte(buf.String()))
	c.lastScrollCount = 0

	c.scrollTop = target
	c.vt = vt
	c.cursorRow = max(0, len(canvas)-1)
	c.hardwareCursorRow = max(0, len(canvas)-1)
}

// windowTop computes the viewport top for a canvas of canvasLen rows on a
// terminal of `height` rows, reconciling the natural bottom anchor with the
// scrollback watermark.
//
// The window top is ALWAYS clamped to the scrollback watermark: rows
// [0, scrollTop) have been emitted into terminal scrollback exactly once and
// the terminal offers no "unscroll" — repainting them onto the visible window
// would duplicate them (the screen-glitching bug at the scrollback boundary).
//
// When the natural anchor dips below the watermark — a mid-transcript shrink
// (a thinking block finalized shorter) or a chrome-band shrink (goal bubble
// cleared) — the clamped window shows blank rows at the top of the
// transcript region. repaintWindow's two-phase layout keeps the chrome band
// pinned at the screen bottom regardless, so the blanks are truthful empty
// space, never displaced chrome.
//
// A deliberate transcript reset (/new, session switch) must call Clear first
// to zero the watermark; otherwise a from-scratch canvas would be anchored at
// a stale watermark instead of rendering fully.
func (c *Compositor) windowTop(canvasLen, height int) int {
	vt := max(0, canvasLen-height)
	if vt < c.scrollTop {
		vt = c.scrollTop
	}
	return vt
}

// scrollTarget computes the new scrollback watermark: the viewport top clamped
// to the transcript (never into the chrome band) and never moved backward.
// It also records the scroll in the frame trace.
func (c *Compositor) scrollTarget(vt, canvasLen int) int {
	contentEnd := canvasLen - c.chromeH
	if contentEnd < 0 {
		contentEnd = 0
	}
	target := min(vt, contentEnd)
	target = max(target, c.scrollTop)
	if c.curTrace != nil {
		c.curTrace.PrevVtop = c.vt
		c.curTrace.NewVtop = vt
		if target > c.scrollTop {
			c.curTrace.Scrolled = true
			c.curTrace.Scroll = target - c.scrollTop
		}
	}
	return target
}

// scrollOffUnstable reports whether the rows about to scroll into scrollback
// (canvas[from, to)) changed identity since the previous frame. The
// incremental scroll emission is only sound when that region is stable: both
// emission paths either push the screen's current top rows (which must equal
// the new canvas rows there) or write the new canvas rows and scroll the
// first of them (which must equal the screen rows). A MID-TRANSCRIPT edit
// above the window — a streaming block that grows ABOVE later-appended
// content, e.g. /quota landing mid-stream — shifts the region and the
// emission would destroy unscrolled rows and emit shifted duplicates
// (Slow performance on very large conversations: the quota-stream
// regression only stayed hidden because every chrome change force-resynced).
//
// Only MALIGNANT instability counts: a row whose content MOVED to a
// different canvas index (a position shift, e.g. a mid-transcript block
// inserted or collapsed above the scroll-off region). A row edited in place
// — its previous content no longer exists anywhere in the transcript, as
// with a live tool widget's ticking elapsed-time text — is benign: the
// physical row that scrolls off carries the right identity (one tick
// stale), and resetting scrollback for it would wipe and re-emit the whole
// transcript on every animation tick (the reset storm). Blank↔content
// transitions are benign too — the bottom-align pad rows being consumed by
// growth, or blank spacers — and the top-down path already handles them.
func (c *Compositor) scrollOffUnstable(canvas []string, to int) bool {
	if c.prevLines == nil {
		return false
	}
	from := max(c.vt, c.scrollTop)
	if from < 0 {
		from = 0
	}
	// Only compare within the PREVIOUS transcript: indices at/above
	// prevContentEnd held the previous chrome band (displaced by any
	// transcript growth) or did not exist yet — both benign.
	prevContentEnd := len(c.prevLines) - c.prevChromeH
	curIndex := map[string]int{} // lazily filled on the first in-region diff
	for i := from; i < to && i < len(canvas); i++ {
		if i < 0 || i >= prevContentEnd {
			continue
		}
		prev := strings.TrimSpace(ansi.Strip(c.prevLines[i]))
		cur := strings.TrimSpace(ansi.Strip(canvas[i]))
		if prev == "" || cur == "" || prev == cur {
			continue
		}
		if len(curIndex) == 0 {
			curIndex = indexCanvasRows(canvas, len(canvas)-c.chromeH)
		}
		if j, ok := curIndex[prev]; ok && j != i {
			return true // position shift: incremental scroll would mis-emit
		}
	}
	return false
}

// windowContentShifted reports whether any row of the visible window
// [vt, vt+height) moved POSITION since the previous frame — i.e. the window's
// content is not a pure scroll of the previous canvas but has an internal
// insertion/deletion (a tool widget growing or shrinking mid-window). In that
// case repaintWindow's unchangedRow skip — which maps screen rows to the
// previous canvas by the pure-scroll delta — is unsound and would leave stale
// and duplicated rows, so the caller must repaint the whole window instead.
//
// Like scrollOffUnstable, only a POSITION shift counts: an in-place edit (a
// live widget's ticking elapsed text) is benign — repaintWindow repaints that
// row because its bytes differ at the SAME index. Blank rows and rows whose
// previous content was in the (now displaced) chrome band are benign too. A
// pure bottom-append stream does NOT trip the guard: the existing window rows
// keep their pure-scroll indices (prev == cur), and the newly revealed bottom
// rows map to indices that held chrome/blank last frame.
func (c *Compositor) windowContentShifted(canvas []string, vt, height int) bool {
	if c.prevLines == nil {
		return false
	}
	// Only an insertion/deletion can shift a window row's canvas index; an
	// in-place edit (no length change — a ticking widget, a same-height text
	// update) leaves every row at its index and is handled correctly by the
	// diff repaint. Bail out unless the transcript grew or shrank so the
	// common in-place streaming/animation case is not misrouted to a full
	// repaint.
	if len(canvas)-c.chromeH == len(c.prevLines)-c.prevChromeH {
		return false
	}
	prevContentEnd := len(c.prevLines) - c.prevChromeH
	windowBottom := min(vt+height, len(canvas)-c.chromeH)
	curIndex := map[string]int{} // lazily filled on the first in-window diff
	for i := vt; i < windowBottom; i++ {
		if i >= prevContentEnd {
			break // at/above the old content end there was chrome or nothing
		}
		// Same-index comparison: under a pure scroll+append the transcript rows
		// keep their canvas indices (new rows append at the bottom), so canvas[i]
		// still holds prevLines[i]. A tool widget growing/shrinking INSIDE the
		// window inserts/deletes rows and shifts every row below it, so canvas[i]
		// no longer matches prevLines[i] there — the exact condition that breaks
		// repaintWindow's unchangedRow skip and leaves duplicate/lost rows around
		// the history↔screen boundary (the reported tool-call duplicate bug).
		prev := strings.TrimSpace(ansi.Strip(c.prevLines[i]))
		cur := strings.TrimSpace(ansi.Strip(canvas[i]))
		if prev == "" || cur == "" || prev == cur {
			continue
		}
		if len(curIndex) == 0 {
			curIndex = indexCanvasRows(canvas, len(canvas)-c.chromeH)
		}
		// The previous content still exists in the transcript but MOVED to a
		// different index (j != i): an internal insertion/deletion shifted it,
		// so the window is not a pure scroll and the diff repaint is unsound.
		// (If it no longer exists anywhere, it was an in-place edit — benign:
		// repaintWindow repaints that row because its bytes differ at index i.)
		if j, ok := curIndex[prev]; ok && j != i {
			return true
		}
	}
	return false
}

// indexCanvasRows maps each transcript row's normalized content (ANSI
// stripped, whitespace trimmed) to its canvas index, excluding the chrome
// band. Rows past contentEnd are chrome, not transcript, so they never
// participate in shift detection. Duplicate rows collapse to one index —
// acceptable, since identical rows scrolling are visually indistinguishable.
func indexCanvasRows(canvas []string, contentEnd int) map[string]int {
	if contentEnd < 0 || contentEnd > len(canvas) {
		contentEnd = len(canvas)
	}
	idx := make(map[string]int, contentEnd)
	for i := 0; i < contentEnd; i++ {
		idx[strings.TrimSpace(ansi.Strip(canvas[i]))] = i
	}
	return idx
}

// advanceScrollback emits the rows that scrolled off since the last frame into
// terminal scrollback, exactly once each. When chrome is pinned the scroll is
// confined to the transcript region via DECSTBM.
func (c *Compositor) advanceScrollback(buf *strings.Builder, canvas []string, target, width, height int) {
	if target <= c.scrollTop {
		return
	}
	from := max(c.vt, c.scrollTop)
	c.emitScrollbackAdvance(buf, canvas, from, target, width, height)
}

// repaintWindow redraws the visible window with absolute CUP, skipping rows
// whose bytes are unchanged since the previous frame.
//
// The window is painted in two phases so the chrome band stays pinned at the
// screen bottom even when vt is clamped above the natural bottom anchor (a
// chrome-band shrink, where the watermark prevents revealing already-scrolled
// rows):
//
//  1. Transcript region: screen rows [1, windowH], mapping to canvas rows
//     [vt, vt+windowH). Rows past contentEnd render as blanks (the transcript
//     genuinely shrank or the watermark clamped above the natural anchor).
//  2. Chrome region: screen rows [windowH+1, height], mapping to canvas rows
//     [contentEnd, len(canvas)). The chrome band is always at the bottom of
//     the screen regardless of where vt lands.
//
// When the canvas is shorter than vt+windowH (it transiently shrank below the
// scrollback watermark), canvas indices past the transcript end render as
// blank rows — clearing the stale rows the taller previous frame left on the
// lower screen, without repainting the already-scrolled rows above vt.
func (c *Compositor) repaintWindow(buf *strings.Builder, canvas []string, vt, width, height int) {
	contentEnd := len(canvas) - c.chromeH
	windowH := height - c.chromeH
	if windowH < 1 {
		windowH = 1
	}
	skipFrom := contentEnd - c.lastScrollCount
	skipTo := contentEnd

	c.repaintTranscriptRows(buf, canvas, vt, windowH, contentEnd, skipFrom, skipTo, width)
	c.repaintChromeRows(buf, canvas, windowH, height, contentEnd, width)
}

// repaintTranscriptRows paints screen rows [1, windowH] from canvas transcript
// rows [vt, vt+windowH), skipping unchanged rows and the scroll-skip window.
func (c *Compositor) repaintTranscriptRows(buf *strings.Builder, canvas []string, vt, windowH, contentEnd, skipFrom, skipTo, width int) {
	for screenRow := 1; screenRow <= windowH; screenRow++ {
		i := vt + screenRow - 1
		line := ""
		if i >= 0 && i < contentEnd {
			line = canvas[i]
		}
		if c.lastScrollCount > 0 && i >= skipFrom && i < skipTo {
			continue
		}
		if c.unchangedRowTranscript(canvas, i, vt) {
			continue
		}
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		buf.WriteString(truncateToWidth(line, width, ""))
		c.traceWroteRow(screenRow)
	}
}

// repaintChromeRows paints screen rows [windowH+1, height] from the canvas
// chrome band [contentEnd, len(canvas)), keeping chrome pinned at the bottom.
func (c *Compositor) repaintChromeRows(buf *strings.Builder, canvas []string, windowH, height, contentEnd, width int) {
	for screenRow := windowH + 1; screenRow <= height; screenRow++ {
		i := contentEnd + (screenRow - windowH - 1)
		line := ""
		if i >= 0 && i < len(canvas) {
			line = canvas[i]
		}
		if c.unchangedRowChrome(canvas, i, screenRow, windowH) {
			continue
		}
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		buf.WriteString(truncateToWidth(line, width, ""))
		c.traceWroteRow(screenRow)
	}
}

// emitScrollbackAdvance advances the viewport from the previous top `from`
// (= the row currently at the top of the screen) to the new top `to`, pushing
// the scrolled-off rows into terminal scrollback exactly once.
//
// The screen currently shows rows [c.vt, c.vt+H) where H is the transcript
// window height; normally c.vt == from, but after a transient mid-stream
// shrink the window can be dipped BELOW the watermark (c.vt < scrollTop =
// from) — in that regime emitSteadyScroll bypasses the physical-identity
// mechanism entirely and re-emits top-down. After the advance the window
// must show [to, to+H). The rows that enter scrollback are [from, to) — the
// old top. The steady mechanism writes only the
// rows that were NOT already on screen, namely [from+H, to+H) (clamped to the
// transcript), at the region bottom, each followed by \n. Every \n scrolls the
// region: the top row (one of the old visible rows, then each freshly written
// row as it reaches the top) moves into scrollback. Writing from the first
// not-yet-visible row guarantees no already-visible row is ever rewritten, so
// nothing is duplicated. When chrome is pinned the scroll is confined to the
// transcript region via DECSTBM.
func (c *Compositor) emitScrollbackAdvance(buf *strings.Builder, canvas []string, from, to, width, height int) {
	if from >= to {
		return
	}
	windowH, scrollBot := c.transcriptWindow(buf, height)
	contentEnd := max(0, len(canvas)-c.chromeH)
	if c.prevLines == nil {
		c.emitFirstFrameScroll(buf, canvas, to, windowH, contentEnd, width)
	} else {
		c.emitSteadyScroll(buf, canvas, from, to, windowH, scrollBot, contentEnd, width)
	}
	if c.chromeH > 0 {
		c.resetScrollRegion(buf)
	}
}

// transcriptWindow returns the transcript window height and the scroll-region
// bottom row for this frame. When chrome is pinned it also confines the scroll
// to the transcript region by emitting DECSTBM into buf.
func (c *Compositor) transcriptWindow(buf *strings.Builder, height int) (windowH, scrollBot int) {
	windowH, scrollBot = height, height
	if c.chromeH > 0 {
		windowH = max(1, height-c.chromeH)
		scrollBot = windowH
		c.setScrollRegion(buf, scrollBot)
	}
	return windowH, scrollBot
}

// emitFirstFrameScroll writes the whole transcript top-down from the region's
// top row, advancing with \n. The screen fills top-to-bottom; once full, each
// further \n scrolls the region's top row into scrollback. Net effect: exactly
// [0, to) in scrollback and [to, to+windowH) on screen, with no out-of-order
// bottom writes, so nothing is duplicated.
func (c *Compositor) emitFirstFrameScroll(buf *strings.Builder, canvas []string, to, windowH, contentEnd, width int) {
	buf.WriteString("\x1b[1;1H")
	writeTo := min(to+windowH, contentEnd)
	for i := 0; i < writeTo; i++ {
		if i > 0 {
			buf.WriteString("\n")
		}
		buf.WriteString("\r\x1b[2K")
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
	}
}

// emitSteadyScroll advances the transcript window from `from` to `to`, pushing
// the scrolled-off rows [from, to) into scrollback exactly once and in order.
//
// The correct mechanism depends on whether the previous window was FULL:
//
//   - Full window (every region row held real content): the canvas layout is
//     stable frame-to-frame, so the newly revealed rows are exactly
//     [from+windowH, to+windowH). Writing those at the region bottom with a
//     line-feed each scrolls the previously-visible rows [from, to) off the
//     top into scrollback naturally. This is the cheap incremental path.
//
//   - Partial / re-anchored window (blank padding, or content that re-flowed
//     because the transcript grew into an empty region): canvas row indices do
//     NOT correspond across frames — content is top-anchored (header) or
//     bottom-anchored (first message) — so index-based "newly revealed" math
//     is unsound and either duplicates rows (header) or drops them (first
//     message). The sound fallback is a top-down re-emit of [from, to+windowH):
//     rows [from, to) scroll into scrollback in order, rows [to, to+windowH)
//     fill the screen. This is exactly once, gapless, for both anchorings.
//
//   - Dipped window (c.vt < scrollTop): a transient mid-stream shrink
//     anchored the window BELOW the scrollback watermark (windowTop's
//     partial-shrink regime), so the physical screen top is c.vt, NOT from
//     (= max(c.vt, scrollTop)). The full-window branch's line-feeds would
//     push the physical top rows [c.vt, c.vt+n) — already in scrollback —
//     in a second time (duplicates) while the watermark skips rows that
//     never physically scrolled (lost rows): the streaming-stutter bug.
//     The top-down path does not depend on physical row identity, so it is
//     the only sound emission while dipped.
func (c *Compositor) emitSteadyScroll(buf *strings.Builder, canvas []string, from, to, windowH, scrollBot, contentEnd, width int) {
	if !c.prevWindowFull(windowH) || c.vt < c.scrollTop {
		c.emitTopDownScroll(buf, canvas, from, to, windowH, contentEnd, width)
		return
	}
	writeFrom := from + windowH
	writeTo := min(to+windowH, contentEnd)
	writeFrom = max(writeFrom, c.scrollTop)
	writeFrom = max(writeFrom, 0)
	buf.WriteString(fmt.Sprintf("\x1b[%d;1H", scrollBot))
	for i := writeFrom; i < writeTo; i++ {
		buf.WriteString("\n")
		buf.WriteString("\r\x1b[2K")
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
	}
}

// emitTopDownScroll re-emits the window top-down from canvas row `from`: it
// homes to the region top, then writes rows [from, writeTo) advancing with
// line-feeds. The first windowH rows fill the screen; each subsequent
// line-feed scrolls the region, pushing one of the leading rows into
// scrollback. Net effect: rows [from, to) land in scrollback in order (exactly
// once) and rows [to, to+windowH) remain on screen. Rows before `from` are
// already in scrollback (watermark) and are not rewritten.
func (c *Compositor) emitTopDownScroll(buf *strings.Builder, canvas []string, from, to, windowH, contentEnd, width int) {
	buf.WriteString("\x1b[1;1H")
	writeTo := min(to+windowH, contentEnd)
	if from < c.scrollTop {
		from = c.scrollTop
	}
	if from < 0 {
		from = 0
	}
	for i := from; i < writeTo; i++ {
		if i > from {
			buf.WriteString("\n")
		}
		buf.WriteString("\r\x1b[2K")
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
	}
}

// prevWindowFull reports whether every transcript region row of the previous
// frame's window held real content (no blank padding). A partial window —
// content top- or bottom-anchored with blanks — has at least one blank region
// row; taking the "visible rows scroll off naturally" path would push those
// blanks into scrollback and lose real content, so it must re-emit everything.
func (c *Compositor) prevWindowFull(windowH int) bool {
	if c.prevLines == nil {
		return false
	}
	// prevLines is the PREVIOUS canvas: its chrome band is prevChromeH rows,
	// not chromeH. Using the current chromeH here miscomputes the previous
	// content end by the chrome delta and could mark a shifted window "full"
	// on stale rows (the watermark desync e6d8a2f worked around with a full
	// reset on every chrome change).
	contentEnd := len(c.prevLines) - c.prevChromeH
	if contentEnd < c.vt+windowH {
		return false
	}
	for r := c.vt; r < c.vt+windowH && r < contentEnd; r++ {
		if strings.TrimSpace(ansi.Strip(c.prevLines[r])) == "" {
			return false
		}
	}
	return true
}

// setScrollRegion emits DECSTBM to confine scrolling to [1, bot] (1-indexed)
// and records it. The cursor is homed by the terminal per the DEC spec.
func (c *Compositor) setScrollRegion(buf *strings.Builder, bot int) {
	if c.regionBot == bot {
		return
	}
	buf.WriteString(fmt.Sprintf("\x1b[1;%dr", bot))
	c.regionBot = bot
}

// resetScrollRegion restores full-screen scrolling (\x1b[r) and records it.
func (c *Compositor) resetScrollRegion(buf *strings.Builder) {
	if c.regionBot == 0 {
		return
	}
	buf.WriteString("\x1b[r")
	c.regionBot = 0
}

// unchangedRowTranscript reports whether transcript canvas row i (at viewport
// top vt) has the same bytes as the row the terminal currently shows at that
// screen position. The previous frame's row at the same screen position was
// prevLines[i - vt + c.vt].
func (c *Compositor) unchangedRowTranscript(canvas []string, i, vt int) bool {
	if c.prevLines == nil {
		return false
	}
	prevIdx := i - vt + c.vt
	if prevIdx < 0 || prevIdx >= len(c.prevLines) {
		return false
	}
	cur := ""
	if i >= 0 && i < len(canvas) {
		cur = canvas[i]
	}
	return c.prevLines[prevIdx] == cur
}

// unchangedRowChrome reports whether chrome canvas row i (at screen row
// screenRow) has the same bytes as the row the terminal currently shows
// there. In the previous frame the chrome band was also pinned at the screen
// bottom, so the same screen row held prevLines at the chrome offset in the
// PREVIOUS canvas.
func (c *Compositor) unchangedRowChrome(canvas []string, i, screenRow, windowH int) bool {
	if c.prevLines == nil {
		return false
	}
	prevChromeH := c.prevChromeH
	prevWindowH := c.prevH - prevChromeH
	if prevWindowH < 1 {
		prevWindowH = 1
	}
	prevContentEnd := len(c.prevLines) - prevChromeH
	prevIdx := prevContentEnd + (screenRow - prevWindowH - 1)
	if prevIdx < 0 || prevIdx >= len(c.prevLines) {
		return false
	}
	cur := ""
	if i >= 0 && i < len(canvas) {
		cur = canvas[i]
	}
	return c.prevLines[prevIdx] == cur
}

// appendCursorSeq writes the hardware-cursor positioning into the SAME
// synced buffer as the frame content (absolute CUP, immune to auto-wrap drift),
// plus a show/hide transition only when the visibility actually changes.
func (c *Compositor) appendCursorSeq(buf *strings.Builder, cp *CursorPos, totalLines, width, vtop, height int) {
	if cp == nil || totalLines <= 0 {
		if c.cursorVisible {
			buf.WriteString("\x1b[?25l")
			c.cursorVisible = false
		}
		return
	}
	targetRow := max(0, min(cp.Row, totalLines-1))
	targetCol := max(0, cp.Col)
	if width > 0 && targetCol >= width {
		targetCol = width - 1
	}
	screenRow := clampRow(targetRow-vtop+1, height)
	buf.WriteString(fmt.Sprintf("\x1b[%d;%dH", screenRow, targetCol+1))
	if !c.cursorVisible {
		buf.WriteString("\x1b[?25h")
		c.cursorVisible = true
	}
	c.hardwareCursorRow = targetRow
}

// clampRow clamps a 1-indexed screen row to [1, height].
func clampRow(row, height int) int {
	if row < 1 {
		return 1
	}
	if row > height {
		return height
	}
	return row
}

// applyLineResets appends a reset sequence to every non-image line in the
// given canvas subrange so SGR state cannot bleed across rows.
func applyLineResets(canvas []string, start, end int) []string {
	for i := start; i < end && i < len(canvas); i++ {
		if canvas[i] == "" {
			continue
		}
		canvas[i] = canvas[i] + "\x1b[0m"
	}
	return canvas
}

func copySlice(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

// FindNode returns the first node with the given name, or nil.
func (f *AgentFrame) FindNode(name string) *AgentNode {
	for i := range f.Nodes {
		if f.Nodes[i].Name == name {
			return &f.Nodes[i]
		}
	}
	return nil
}

// FindNodeByType returns the first node with the given type prefix, or nil.
func (f *AgentFrame) FindNodeByType(typePrefix string) *AgentNode {
	for i := range f.Nodes {
		if strings.Contains(f.Nodes[i].Type, typePrefix) {
			return &f.Nodes[i]
		}
	}
	return nil
}

// FocusedNode returns the first focused node, or nil.
func (f *AgentFrame) FocusedNode() *AgentNode {
	for i := range f.Nodes {
		if f.Nodes[i].Focused {
			return &f.Nodes[i]
		}
	}
	return nil
}

// CursorNode returns the node that contains the absolute cursor, or nil if
// the cursor is hidden or no node overlaps it.
func (f *AgentFrame) CursorNode() *AgentNode {
	if f.Cursor == nil {
		return nil
	}
	for i := range f.Nodes {
		n := &f.Nodes[i]
		if f.Cursor.Row >= n.Rect.Y && f.Cursor.Row < n.Rect.Y+n.Rect.H &&
			f.Cursor.Col >= n.Rect.X && f.Cursor.Col < n.Rect.X+n.Rect.W {
			return n
		}
	}
	return nil
}

// Dump returns a human-readable description of the agentic screen model for
// debugging test failures. It includes the terminal size, cursor, and every
// node with its bounds and content.
func (f AgentFrame) Dump() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("AgentFrame %dx%d\n", f.Width, f.Height))
	if f.Cursor != nil {
		b.WriteString(fmt.Sprintf("cursor: (%d,%d)\n", f.Cursor.Row, f.Cursor.Col))
	} else {
		b.WriteString("cursor: hidden\n")
	}
	for _, n := range f.Nodes {
		focus := ""
		if n.Focused {
			focus = " [focused]"
		}
		b.WriteString(fmt.Sprintf("node %s (%s) rect=%+v%s\n", n.Name, n.Type, n.Rect, focus))
		for _, line := range strings.Split(n.Text, "\n") {
			b.WriteString(fmt.Sprintf("  %q\n", line))
		}
	}
	return b.String()
}

// fillNodeText sets each node's Text by ANSI-stripping its matching layer's
// content. agentNodeFor defers this O(n) Join+Strip so the live render path
// (which never builds an AgentFrame) does not pay it every frame for the chat
// layer; it is paid once here, only when AI tooling requests the DOM.
func fillNodeText(nodes []AgentNode, layers []Layer) []AgentNode {
	if len(nodes) == 0 {
		return nodes
	}
	textByLayer := make(map[string]string, len(layers))
	for _, l := range layers {
		if _, ok := textByLayer[l.Name]; ok {
			continue
		}
		textByLayer[l.Name] = ansi.Strip(strings.Join(l.Content, "\n"))
	}
	for i := range nodes {
		if text, ok := textByLayer[nodes[i].Name]; ok {
			nodes[i].Text = text
		}
	}
	return nodes
}
