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
}

// compose builds the virtual-buffer canvas from the Scene's layers: each
// layer's Content is placed at its Rect, higher Z overwriting lower Z where
// they overlap. Only the region that is currently visible or was visible in
// the previous frame is actually written, so large off-screen histories do
// not burn CPU per frame, while large scrolls still have the gap lines
// needed to populate the terminal scrollback. Returns the canvas and whether
// any overlay layer is present.
//
// This is the single place that decides pixel placement. Keeping it here (in
// the Compositor) means components only declare position/size/content and
// never reason about overlaps or composition order.
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

	// Base layers first.
	for _, l := range s.Layers {
		if l.Kind != LayerOverlay {
			placeLayer(canvas, l, s.TerminalW, placeStart, visibleEnd)
		}
	}

	// Overlays, positioned relative to the visible viewport.
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
		for i, c := range l.Content {
			al.Lines[i] = ansi.Strip(c)
		}
		al.Visible = l.Rect.Y < vBottom && (l.Rect.Y+l.Rect.H) > vTop
		frame.Layers = append(frame.Layers, al)
	}

	// Visible viewport as plain text, reading order. Overlays are already
	// composited into the canvas by compose(), so stripping the canvas rows
	// in [vTop, vBottom) yields what the user actually sees.
	frame.Visible = make([]string, 0, vBottom-vTop)
	for y := vTop; y < vBottom; y++ {
		frame.Visible = append(frame.Visible, strings.TrimRight(ansi.Strip(canvas[y]), " "))
	}
	return frame
}

// FindNode returns the first node with the given name, or nil if none exists.
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

// Compositor owns ALL terminal-protocol concerns: it composes a Scene's
// layers into a canvas, diffs against the previous frame, and emits the
// escape sequences (synchronized output, cursor movement, line clears,
// scrollback scrolling, hardware-cursor positioning). It is the single place
// that knows about terminal protocol. The TUI never touches protocol state.
//
// The diff math is kept cohesive with the render logic itself.
// frame-local viewport/cursor state threaded through one control flow.
type Compositor struct {
	terminal Terminal

	mu sync.Mutex // serializes Render/Restore/Buffer against each other

	// Diff baseline / tracking state, owned solely by the Compositor.
	prevLines           []string
	prevW, prevH        int
	cursorRow           int
	hardwareCursorRow   int
	previousViewportTop int
	viewportTop         int // last frame's viewport top (for absolute cursor math)
	maxLinesRendered    int
	fullRedrawCount     int
	firstScrollDone     bool // true after the first viewport scroll; affects scrollback population
	clearOnShrink       bool // when true, shrinks that overflow scrollback trigger a full redraw

	// cursorVisible tracks the terminal's cursor-show state so we only emit
	// \x1b[?25h / \x1b[?25l on a real transition, never as a redundant per-frame
	// write. It is updated solely inside the synced frame buffers.
	cursorVisible bool
}

// NewCompositor creates a Compositor bound to a Terminal.
func NewCompositor(term Terminal) *Compositor {
	return &Compositor{terminal: term}
}

// FullRedrawCount exposes the number of full redraws (diagnostics/tests).
func (c *Compositor) FullRedrawCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.fullRedrawCount
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
	if c.cursorRow > 0 {
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H", c.cursorRow+1))
	} else if len(c.prevLines) > 0 {
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H", len(c.prevLines)+1))
	}
	buf.WriteString("\r\n")
	c.terminal.Write([]byte(buf.String()))
	c.terminal.ShowCursor()
}

// Render composes the Scene's layers into a canvas, diffs against the previous
// frame, and emits the terminal update.
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

	canvas, hasOverlay := scene.compose(c.previousViewportTop)

	fl := c.computeFrameLocals(width, height)
	fullRender := func(clear bool) { c.fullFrame(canvas, scene.Cursor, width, height, clear) }
	resizeRender := func() { c.resizeFrame(canvas, scene.Cursor, width, height) }

	if c.earlyFullRenderPath(canvas, hasOverlay, fl.widthChanged, fl.heightChanged, height, fullRender, resizeRender) {
		return
	}
	c.renderChangePath(canvas, hasOverlay, scene.Cursor, fl, fullRender)
}

// frameLocals holds viewport/cursor tracking state for one frame, threaded as
// a single unit (not split across fields).
type frameLocals struct {
	width, height                                   int
	widthChanged, heightChanged                     bool
	prevViewportTop, viewportTop, hardwareCursorRow int
	computeLineDiff                                 func(targetRow int) int
}

func (c *Compositor) computeFrameLocals(width, height int) *frameLocals {
	widthChanged := c.prevW != 0 && c.prevW != width
	heightChanged := c.prevH != 0 && c.prevH != height
	prevBufferLength := height
	if c.previousViewportTop > 0 || c.prevH > 0 {
		prevBufferLength = c.previousViewportTop + c.prevH
	}
	prevViewportTop := c.previousViewportTop
	if heightChanged {
		prevViewportTop = max(0, prevBufferLength-height)
	}
	fl := &frameLocals{
		width: width, height: height,
		widthChanged: widthChanged, heightChanged: heightChanged,
		prevViewportTop:   prevViewportTop,
		viewportTop:       prevViewportTop,
		hardwareCursorRow: c.hardwareCursorRow,
	}
	fl.computeLineDiff = func(targetRow int) int {
		currentScreenRow := fl.hardwareCursorRow - fl.prevViewportTop
		targetScreenRow := targetRow - fl.viewportTop
		return targetScreenRow - currentScreenRow
	}
	return fl
}

func (c *Compositor) earlyFullRenderPath(canvas []string, hasOverlay, widthChanged, heightChanged bool, height int, fullRender func(bool), resizeRender func()) bool {
	if len(c.prevLines) == 0 && !widthChanged && !heightChanged {
		fullRender(false)
		return true
	}
	if widthChanged || heightChanged {
		// Resize: repaint only the visible viewport and preserve scrollback
		// (no \x1b[3J, no full re-emit of the transcript). See resizeFrame.
		resizeRender()
		return true
	}
	// Shrink guard: a full redraw is only required when the shrink could have
	// left the SCROLLBACK stale, i.e. when we have previously rendered more
	// lines than fit on screen (maxLinesRendered > height). Pure in-viewport
	// shrinks are handled correctly by the differential path's trailing-line
	// clear (writeDifferential), so they no longer force an O(history) full
	// redraw on every later frame. !hasOverlay because overlays are
	// viewport-relative and must not be disturbed by a base clear.
	if c.clearOnShrink && len(canvas) < c.maxLinesRendered && c.maxLinesRendered > height && !hasOverlay {
		fullRender(true)
		return true
	}
	return false
}

func (c *Compositor) visibleRegionDiff(canvas []string, newVTop int, fl *frameLocals) (firstV, lastV int) {
	firstV, lastV = -1, -1
	height := fl.height
	prevVTop := fl.prevViewportTop
	prevLen := len(c.prevLines)
	if height <= 0 {
		return
	}
	if newVTop < 0 {
		newVTop = 0
	}
	if prevVTop < 0 {
		prevVTop = 0
	}

	prevStart := min(prevVTop, prevLen)
	prevEnd := min(prevVTop+height, prevLen)
	newStart := min(newVTop, len(canvas))
	newEnd := min(newVTop+height, len(canvas))

	prevVisible := c.prevLines[prevStart:prevEnd]
	newVisible := canvas[newStart:newEnd]

	scroll := newVTop - prevVTop
	if scroll <= 0 {
		// No downward scroll (or scrolled up): compare visible regions directly.
		return firstLastDiff(prevVisible, newVisible)
	}

	// Downward scroll: old visible content shifts up by `scroll`; the bottom
	// `scroll` rows are new. Compare the shifted overlap; anything beyond it
	// is considered changed because the terminal will expose blank rows after
	// the scroll.
	if scroll >= len(newVisible) {
		return 0, len(newVisible) - 1
	}
	shiftedOld := prevVisible[scroll:]
	for i := 0; i < len(newVisible); i++ {
		var oldLine string
		if i < len(shiftedOld) {
			oldLine = shiftedOld[i]
		}
		if newVisible[i] != oldLine {
			if firstV < 0 {
				firstV = i
			}
			lastV = i
		}
	}
	return
}

func (c *Compositor) renderChangePath(canvas []string, hasOverlay bool, cursor *CursorPos, fl *frameLocals, fullRender func(bool)) {
	newVTop := max(0, len(canvas)-fl.height)
	firstV, lastV := c.visibleRegionDiff(canvas, newVTop, fl)

	if firstV < 0 {
		// No visible content changed; only the cursor may need repositioning.
		// Emit it in its own synced frame so even a cursor-only update is atomic.
		c.renderCursorOnly(cursor, len(canvas), fl.width, newVTop, fl.height)
		c.previousViewportTop = newVTop
		c.prevH = fl.height
		return
	}

	firstChanged := firstV + newVTop
	lastChanged := lastV + newVTop

	if firstChanged >= len(canvas) && len(c.prevLines) > len(canvas) {
		if c.renderDeletedLines(canvas, cursor, fl.prevViewportTop, fl.height, fl.computeLineDiff, fullRender) {
			return
		}
	}
	if needsFullRedrawForChange(firstChanged, fl.prevViewportTop, fl.viewportTop, len(canvas), fl.height, hasOverlay) {
		fullRender(true)
		return
	}
	finalCursorRow := c.writeDifferential(canvas, firstChanged, lastChanged, fl.width,
		&fl.prevViewportTop, &fl.viewportTop, fl.height, cursor)

	c.cursorRow = max(0, len(canvas)-1)
	if cursor == nil {
		// writeDifferential's appendCursorSeq already set hardwareCursorRow when
		// the cursor is shown; only fall back to finalCursorRow when hidden.
		c.hardwareCursorRow = finalCursorRow
	}
	c.viewportTop = fl.viewportTop
	c.maxLinesRendered = max(c.maxLinesRendered, len(canvas))
	c.previousViewportTop = max(fl.prevViewportTop, finalCursorRow-fl.height+1)
	c.prevLines = copySlice(canvas)
	c.prevW = fl.width
	c.prevH = fl.height
}

func (c *Compositor) fullFrame(canvas []string, cursor *CursorPos, width, height int, clear bool) {
	c.fullRedrawCount++
	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	if clear {
		buf.WriteString("\x1b[2J\x1b[H\x1b[3J")
	}
	for i, line := range canvas {
		if i > 0 {
			buf.WriteString("\r\n")
		}
		if vw := visibleWidth(line); vw > width {
			line = truncateToWidth(line, width, "")
		}
		buf.WriteString(line)
	}
	bufferLength := max(height, len(canvas))
	vtop := max(0, bufferLength-height)
	// Cursor repositioning lives inside the sync (see writeDifferential).
	c.appendCursorSeq(&buf, cursor, len(canvas), width, vtop, height)
	buf.WriteString("\x1b[?2026l")
	c.terminal.Write([]byte(buf.String()))

	c.applyFrameTracking(canvas, cursor, width, height, clear)
}

// resizeFrame repaints only the visible viewport after a terminal size change.
// Unlike fullFrame(clear=true), it does NOT emit \x1b[3J (so existing
// scrollback is preserved) and writes only the last `height` canvas rows
// instead of the whole transcript. On resize the terminal already holds the
// prior scrollback; the reflowed visible rows are all that must be repainted,
// so we avoid re-emitting the entire history (which was slow on long sessions
// and discarded the user's scroll position).
func (c *Compositor) resizeFrame(canvas []string, cursor *CursorPos, width, height int) {
	c.fullRedrawCount++
	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	buf.WriteString("\x1b[2J\x1b[H") // clear screen only; keep scrollback (no \x1b[3J)
	start := len(canvas) - height
	if start < 0 {
		start = 0
	}
	for i := start; i < len(canvas); i++ {
		if i > start {
			buf.WriteString("\r\n")
		}
		line := canvas[i]
		if vw := visibleWidth(line); vw > width {
			line = truncateToWidth(line, width, "")
		}
		buf.WriteString(line)
	}
	vtop := max(0, len(canvas)-height)
	c.appendCursorSeq(&buf, cursor, len(canvas), width, vtop, height)
	buf.WriteString("\x1b[?2026l")
	c.terminal.Write([]byte(buf.String()))

	// clear=false so maxLinesRendered (the shrink-detection high-water mark) is
	// preserved rather than reset to the visible-only length.
	c.applyFrameTracking(canvas, cursor, width, height, false)
}

// applyFrameTracking updates the compositor's diff baseline and viewport
// anchors after a full-screen repaint (fullFrame or resizeFrame). It is the
// shared tail of both paths so they cannot drift in what they record.
func (c *Compositor) applyFrameTracking(canvas []string, cursor *CursorPos, width, height int, clear bool) {
	c.cursorRow = max(0, len(canvas)-1)
	c.hardwareCursorRow = c.cursorRow
	if clear {
		c.maxLinesRendered = len(canvas)
	} else {
		c.maxLinesRendered = max(c.maxLinesRendered, len(canvas))
	}
	vtop := max(0, len(canvas)-height)
	c.previousViewportTop = vtop
	c.viewportTop = vtop
	c.firstScrollDone = len(canvas) > height
	c.prevH = height
	c.prevLines = copySlice(canvas)
	c.prevW = width
}

func (c *Compositor) renderDeletedLines(canvas []string, cursor *CursorPos, prevViewportTop, height int,
	computeLineDiff func(int) int, fullRender func(bool)) bool {

	targetRow := max(0, len(canvas)-1)
	if targetRow < prevViewportTop {
		fullRender(true)
		return true
	}
	extraLines := len(c.prevLines) - len(canvas)
	if extraLines > height {
		fullRender(true)
		return true
	}
	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	lineDiff := computeLineDiff(targetRow)
	if lineDiff > 0 {
		buf.WriteString(fmt.Sprintf("\x1b[%dB", lineDiff))
	} else if lineDiff < 0 {
		buf.WriteString(fmt.Sprintf("\x1b[%dA", -lineDiff))
	}
	buf.WriteString("\r")
	clearStartOffset := 0
	if len(canvas) > 0 {
		clearStartOffset = 1
	}
	if extraLines > 0 && clearStartOffset > 0 {
		buf.WriteString("\x1b[1B")
	}
	for i := 0; i < extraLines; i++ {
		buf.WriteString("\r\x1b[2K")
		if i < extraLines-1 {
			buf.WriteString("\x1b[1B")
		}
	}
	moveBack := max(0, extraLines-1+clearStartOffset)
	if moveBack > 0 {
		buf.WriteString(fmt.Sprintf("\x1b[%dA", moveBack))
	}
	buf.WriteString("\x1b[?2026l")
	c.terminal.Write([]byte(buf.String()))
	c.cursorRow = targetRow
	c.hardwareCursorRow = targetRow
	_ = cursor
	return false
}

// writeDifferential writes the changed line range using ABSOLUTE cursor
// positioning (CUP) for every changed line. This is the root-cause fix for
// streaming "ghosting": full-width-padded lines trigger the terminal's DEC
// deferred auto-wrap, which desyncs relative cursor moves (CUU/CUD) from the
// real cursor — so updated rows were written at drifting positions and old
// pixels remained. Absolute CUP makes each row's position a pure function of
// (row - viewportTop), immune to any cursor drift.
//
// Viewport follow: because absolute CUP cannot lean on the terminal's native
// \r\n scroll (that is a RELATIVE move and suffers the same auto-wrap drift),
// the viewport must be advanced EXPLICITLY when content grows past the bottom.
// We bottom-anchor viewportTop to max(0, len(canvas)-height) — the same anchor
// fullFrame uses — emitting scrollback newlines before the CUP write loop.
// Without this, overflow lines would be clamped to the bottom screen row and
// clobber each other (the streaming "stacked on a couple lines" regression).
//
// For a single huge append (changed region starts above the bottom-anchored
// viewport), the lines that fall above the viewport were never on screen, so
// only the visible tail is drawn. This never erases existing scrollback (the
// namesake invariant of TestChatLargeAppendScrollsWithoutErasingScrollback):
// the scroll newlines push the previously-visible rows into scrollback, and no
// full redraw / \x1b[2J / \x1b[3J is emitted.
// emitViewportScroll advances the terminal viewport from prevVtop to newVtop,
// pushing the previous on-screen rows into scrollback. For a large append
// (more new lines than fit on screen), the newly-added lines above the new
// viewport were never on screen; bare newlines would push BLANK rows into
// scrollback and the user could not scroll back to them. So each such gap line
// is written as real content (clear bottom row, write, newline to scroll it
// into scrollback). The gap starts at firstChanged so content that shifted
// into indices previously covered by stale on-screen rows is preserved.
//
// For the very first scroll of a session, the previous viewport is empty or
// only partially filled, so bare newlines would push blank rows into
// scrollback. On that first scroll we write every scrolled-off row directly
// as content, ensuring the full transcript is recoverable.
func (c *Compositor) emitViewportScroll(canvas []string, firstChanged, width, prevVtop, newVtop, height int) {
	scroll := newVtop - prevVtop
	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	buf.WriteString(fmt.Sprintf("\x1b[%d;1H", height))
	if !c.firstScrollDone {
		emitFirstScroll(&buf, canvas, width)
	} else if scroll > height && height > 0 {
		emitLargeScroll(&buf, canvas, firstChanged, width, newVtop, height, prevVtop)
	} else {
		buf.WriteString(strings.Repeat("\n", scroll))
	}
	buf.WriteString("\x1b[?2026l")
	c.terminal.Write([]byte(buf.String()))
	c.firstScrollDone = true
}

// emitFirstScroll writes the whole canvas from the top with \r\n so the
// terminal naturally scrolls and populates scrollback. Used for the first
// viewport advance of a session when there is no prior full viewport to push.
func emitFirstScroll(buf *strings.Builder, canvas []string, width int) {
	for i := 0; i < len(canvas); i++ {
		line := canvas[i]
		if vw := visibleWidth(line); vw > width {
			line = truncateToWidth(line, width, "")
		}
		if i > 0 {
			buf.WriteString("\r\n")
		}
		buf.WriteString(line)
	}
}

// emitLargeScroll handles a viewport advance larger than one screen by
// pushing the previous viewport into scrollback with bare newlines, then
// writing each newly-added line above the new viewport as real content so
// it too ends up in scrollback.
func emitLargeScroll(buf *strings.Builder, canvas []string, firstChanged, width, newVtop, height, prevVtop int) {
	buf.WriteString(strings.Repeat("\n", height))
	gapStart := max(0, prevVtop+height)
	for i := gapStart; i < newVtop; i++ {
		line := canvas[i]
		if vw := visibleWidth(line); vw > width {
			line = truncateToWidth(line, width, "")
		}
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", height))
		buf.WriteString(line)
		buf.WriteString("\n")
	}
}

func (c *Compositor) writeDifferential(canvas []string, firstChanged, lastChanged, width int,
	prevViewportTop, viewportTop *int, height int, cursor *CursorPos) int {

	// Bottom-anchor the viewport to the new content tail before the CUP write
	// loop. This is what makes streaming scroll instead of clobber.
	desiredViewportTop := len(canvas) - height
	if desiredViewportTop < 0 {
		desiredViewportTop = 0
	}
	if desiredViewportTop > *prevViewportTop {
		c.emitViewportScroll(canvas, firstChanged, width, *prevViewportTop, desiredViewportTop, height)
		*prevViewportTop = desiredViewportTop
		*viewportTop = desiredViewportTop
	}

	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	vtop := *viewportTop

	// Only lines inside the viewport can be drawn with CUP. Lines above it (a
	// single huge append whose head scrolled past) are skipped: they were never
	// displayed, and drawing them now would write into rows that belong to the
	// already-scrolled content. Clamping the start here also guarantees every
	// emitted screenRow is within [1, height], so no overflow clobber can occur.
	renderStart := firstChanged
	if renderStart < vtop {
		renderStart = vtop
	}
	renderEnd := min(lastChanged, len(canvas)-1)
	for i := renderStart; i <= renderEnd; i++ {
		if !c.lineNeedsRedraw(canvas, i) {
			continue
		}
		screenRow := clampRow(i-vtop+1, height)
		buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", screenRow))
		line := canvas[i]
		if vw := visibleWidth(line); vw > width {
			line = truncateToWidth(line, width, "")
		}
		buf.WriteString(line)
	}
	finalCursorRow := renderEnd
	// Clear extra trailing lines if the buffer shrank (absolute CUP per line).
	// Only clear rows that are actually on screen: when the canvas shrank to fit
	// (canvas <= height), the freed lines are above/below the visible region and
	// must not be touched, or the last visible line (e.g. the input) gets wiped.
	if extra := len(c.prevLines) - len(canvas); extra > 0 {
		for k := 0; k < extra; k++ {
			row := (len(canvas) + k) - vtop + 1
			if row < 1 || row > height {
				continue
			}
			buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", row))
		}
		finalCursorRow = max(0, len(canvas)-1)
	}
	// Fold the hardware-cursor repositioning into the SAME synced region so the
	// cursor (and thus the input line visually) is restored atomically with the
	// content under CSI 2026, instead of in a separate unsynchronized write
	// after the sync closed (which caused a per-frame cursor flash).
	c.appendCursorSeq(&buf, cursor, len(canvas), width, vtop, height)
	buf.WriteString("\x1b[?2026l")
	c.terminal.Write([]byte(buf.String()))
	return finalCursorRow
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

// lineNeedsRedraw reports whether canvas line i must be rewritten this frame.
// Lines that were never drawn before, or whose bytes changed from the previous
// frame, are redrawn. Unchanged lines are skipped so the input/footer stay
// stable while streaming content scrolls above them.
func (c *Compositor) lineNeedsRedraw(canvas []string, i int) bool {
	if i >= len(c.prevLines) {
		return true
	}
	return c.prevLines[i] != canvas[i]
}

// appendCursorSeq writes the hardware-cursor positioning into the SAME
// synced buffer as the frame content (absolute CUP, immune to auto-wrap drift),
// plus a show/hide transition only when the visibility actually changes. It is
// the single place that emits cursor escapes, and it must run inside an open
// \x1b[?2026h ... \x1b[?2026l region so the cursor is restored atomically with
// the content — the root-cause fix for the input-line cursor flicker that the
// former separate, unsynchronized positionHardwareCursor write caused.
//
// vtop/height are passed explicitly (rather than read from c.viewportTop /
// c.prevH) because the cursor seq is emitted before those fields are updated
// for the new frame.
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
	// A cursor at column == width (i.e. one past the last visible cell, which
	// happens when the input exactly fills a wrapped line) forces the terminal
	// to wrap the hardware cursor onto the next physical line. Clamp it to the
	// last column so the cursor stays on the current line instead of jumping.
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

// renderCursorOnly emits a cursor repositioning in its own minimal synced
// frame, used on the no-visible-change path where only the cursor may have
// moved. Keeping it synced avoids an unsynchronized cursor write leaking
// between frames.
func (c *Compositor) renderCursorOnly(cp *CursorPos, totalLines, width, vtop, height int) {
	var buf strings.Builder
	buf.WriteString("\x1b[?2026h")
	c.appendCursorSeq(&buf, cp, totalLines, width, vtop, height)
	buf.WriteString("\x1b[?2026l")
	c.terminal.Write([]byte(buf.String()))
}

// applyLineResets appends a reset sequence to every non-image line in the
// visible region [viewportStart, visibleEnd) so SGR attributes and OSC 8
// links do not bleed across lines. Off-screen lines are left untouched.
func applyLineResets(lines []string, viewportStart, visibleEnd int) []string {
	const reset = SEGMENT_RESET
	if viewportStart < 0 {
		viewportStart = 0
	}
	if visibleEnd > len(lines) {
		visibleEnd = len(lines)
	}
	if visibleEnd <= viewportStart {
		return lines
	}
	for i := viewportStart; i < visibleEnd; i++ {
		line := lines[i]
		if isImageLine(line) {
			continue
		}
		line = normalizeTerminalOutput(line)
		if strings.HasSuffix(line, ansi.Reset) {
			continue
		}
		lines[i] = line + reset
	}
	return lines
}

func needsFullRedrawForChange(firstChanged, prevViewportTop, viewportTop, newLen, height int, overlayOpen bool) bool {
	// When the canvas fits on screen, the whole conversation is already visible:
	// there is no scrollback region to reveal and the viewport cannot scroll up.
	// A change there is just new content to rewrite differentially. Forcing a
	// full redraw would emit a screen clear and wipe the terminal scrollback —
	// the shrink regression (clear/collapse after overflow).
	if newLen <= height {
		return false
	}
	if firstChanged < prevViewportTop {
		return true
	}
	if overlayOpen {
		contentViewportTop := max(0, newLen-height)
		if firstChanged < contentViewportTop || contentViewportTop > prevViewportTop {
			return true
		}
	}
	return viewportTop < prevViewportTop
}

func firstLastDiff(old, new []string) (first, last int) {
	first = -1
	last = -1
	maxLen := len(old)
	if len(new) > maxLen {
		maxLen = len(new)
	}
	for i := 0; i < maxLen; i++ {
		var o, n string
		if i < len(old) {
			o = old[i]
		}
		if i < len(new) {
			n = new[i]
		}
		if o != n {
			if first < 0 {
				first = i
			}
			last = i
		}
	}
	return
}

func copySlice(src []string) []string {
	dst := make([]string, len(src))
	copy(dst, src)
	return dst
}

// isImageLine checks if a line contains a Kitty image protocol sequence.
func isImageLine(line string) bool {
	return strings.Contains(line, "\x1b_G")
}

// normalizeTerminalOutput normalizes Thai/Lao AM vowels for compatibility.
func normalizeTerminalOutput(s string) string {
	s = strings.ReplaceAll(s, "\u0e33", "\u0e4d\u0e32")
	s = strings.ReplaceAll(s, "\u0eb3", "\u0ecd\u0eb2")
	return s
}
