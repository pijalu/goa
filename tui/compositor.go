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
// placed at its Rect. Only the region that is currently visible or was visible
// in the previous frame is actually written, so large off-screen histories do
// not burn CPU per frame, while large scrolls still have the gap lines needed
// to populate the terminal scrollback. Returns the canvas and whether any
// overlay layer is present.
//
// This is the single place that decides pixel placement of base content.
// Overlays are composited separately (viewport-relative) by the caller's
// render path, never here.
func (s *Scene) compose(prevViewportTop int) (canvas []string, hasOverlay bool) {
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
	placeStart := viewportStart
	if prevViewportTop >= 0 && prevViewportTop < viewportStart {
		placeStart = prevViewportTop
	}
	// On a WIDTH change the compositor wipes the terminal scrollback (\x1b[3J)
	// and re-emits the whole off-screen transcript from this canvas, so every
	// row must be materialized — the steady-state optimization (skip rows above
	// placeStart) would re-emit them as blanks, erasing visible history.
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
	// regionBot is the DECSTBM scroll-region bottom currently in effect on the
	// terminal (1-indexed; region top is always row 1), or 0 when no region is
	// set (full-screen scroll). When chromeH > 0 the compositor confines the
	// line-feed scroll to the transcript region [1, height-chromeH] so that
	// emitting scrollback rows never moves the pinned chrome below the region.
	regionBot int

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

	c.chromeH = scene.ChromeHeight
	if c.chromeH < 0 {
		c.chromeH = 0
	}
	resized := (c.prevW != 0 && c.prevW != width) || (c.prevH != 0 && c.prevH != height)
	widthChanged := c.prevW != 0 && c.prevW != width
	first := c.prevLines == nil
	// Tell compose whether this is a width-change frame BEFORE building the
	// canvas: a width change triggers the scrollback-reset path, which needs
	// the full off-screen transcript materialized.
	scene.WidthChanged = widthChanged
	canvas, hasOverlay := scene.compose(c.vt)
	c.beginTrace(scene, canvas, width, height)
	defer c.emitTrace()

	switch {
	case first:
		// First frame: InitialClear already wiped screen+scrollback; drawWindow
		// emits any off-screen rows into scrollback then draws the window.
		c.drawWindow(canvas, scene.Cursor, width, height)
	case widthChanged:
		// Width change: the terminal's scrollback still holds rows laid out at
		// the OLD width (line wrap differs), so it no longer matches the new
		// layout. Reset scrollback and re-emit every off-screen row at the new
		// width so history reads correctly, then repaint the window.
		c.drawWindowResetScrollback(canvas, scene.Cursor, width, height)
	case resized || hasOverlay:
		// Height-only resize or overlay: drawWindow emits scrolled-off rows
		// then repaints the visible window in place (no screen wipe — the
		// per-row repaint already replaces every row; see drawWindow).
		c.drawWindow(canvas, scene.Cursor, width, height)
	default:
		c.renderDiff(canvas, scene.Cursor, width, height)
	}

	c.prevLines = copySlice(canvas)
	c.prevW = width
	c.prevH = height
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
// (bugs.md "Mascot/logo redraw", HIGH). In-place row replacement — the same
// strategy renderDiff uses — is flicker-free and leaves no stale content:
// every row is overwritten, and a shorter canvas shifts the window bottom up
// rather than leaving residue.
func (c *Compositor) drawWindow(canvas []string, cursor *CursorPos, width, height int) {
	c.setTracePath("full")
	c.fullRedrawCount++
	vt := max(0, len(canvas)-height)

	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	c.appendOverflow(&buf, canvas, width, height)
	for i := vt; i < len(canvas); i++ {
		screenRow := i - vt + 1
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
	for i := vt; i < len(canvas); i++ {
		screenRow := i - vt + 1
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
	vt := max(0, len(canvas)-height)
	target := c.scrollTarget(vt, len(canvas))

	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	c.advanceScrollback(&buf, canvas, target, width, height)
	c.repaintWindow(&buf, canvas, vt, width, height)
	c.appendCursorSeq(&buf, cursor, len(canvas), width, vt, height)
	buf.WriteString("\x1b[?2026l")
	c.terminal.Write([]byte(buf.String()))

	c.scrollTop = target
	c.vt = vt
	c.cursorRow = max(0, len(canvas)-1)
	c.hardwareCursorRow = max(0, len(canvas)-1)
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
func (c *Compositor) repaintWindow(buf *strings.Builder, canvas []string, vt, width, height int) {
	for i := vt; i < len(canvas); i++ {
		screenRow := i - vt + 1
		if screenRow < 1 || screenRow > height {
			continue
		}
		if c.unchangedRow(canvas, i, vt) {
			continue
		}
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		buf.WriteString(truncateToWidth(canvas[i], width, ""))
		c.traceWroteRow(screenRow)
	}
}

// emitScrollbackAdvance advances the viewport from the previous top `from`
// (= the row currently at the top of the screen) to the new top `to`, pushing
// the scrolled-off rows into terminal scrollback exactly once.
//
// The screen currently shows rows [from, from+H) where H is the transcript
// window height. After the advance it must show [to, to+H). The rows that
// enter scrollback are [from, to) — the old top. The mechanism writes only the
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
func (c *Compositor) emitSteadyScroll(buf *strings.Builder, canvas []string, from, to, windowH, scrollBot, contentEnd, width int) {
	if !c.prevWindowFull(windowH) {
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
	contentEnd := len(c.prevLines) - c.chromeH
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

// unchangedRow reports whether canvas row i (in the current window at viewport
// top vt) has the same bytes as the row the terminal currently shows there.
// The previous frame's row that was shown at this screen position was
// prevLines[i] adjusted by the viewport-top delta between frames.
func (c *Compositor) unchangedRow(canvas []string, i, vt int) bool {
	if c.prevLines == nil {
		return false
	}
	// Canvas row i is at screen row i-vt. In the previous frame the same screen
	// row showed prevLines[i - vt + c.vt].
	prevIdx := i - vt + c.vt
	if prevIdx < 0 || prevIdx >= len(c.prevLines) {
		return false
	}
	return c.prevLines[prevIdx] == canvas[i]
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
