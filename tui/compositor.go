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
// size, the layers, and the input cursor. It is the single source of truth
// consumed by the Compositor (terminal bytes), the AgentView (plain text for
// AI tooling), and tests.
type Scene struct {
	TerminalW int
	TerminalH int
	Layers    []Layer    // base layers first, then overlays; ordering within equal Z is stable
	Cursor    *CursorPos // nil hides the hardware cursor
}

// compose builds the virtual-buffer canvas from the Scene's layers: each
// layer's Content is placed at its Rect, higher Z overwriting lower Z where
// they overlap. Returns the canvas lines (height = max Y+H over layers) and
// whether any overlay layer is present (affects the diff strategy).
//
// This is the single place that decides pixel placement. Keeping it here (in
// the Compositor) means components only declare position/size/content and
// never reason about overlaps or composition order.
func (s *Scene) compose() (canvas []string, hasOverlay bool) {
	height := baseCanvasHeight(s.Layers)
	if height == 0 {
		height = 1
	}
	canvas = make([]string, height)

	// Base layers first.
	for _, l := range s.Layers {
		if l.Kind != LayerOverlay {
			placeLayer(canvas, l, s.TerminalW)
		}
	}

	// Overlays, positioned relative to the visible viewport.
	overlays := overlaysOf(s.Layers)
	if len(overlays) == 0 {
		return canvas, false
	}
	canvas = placeOverlays(canvas, overlays, height, s.TerminalH, s.TerminalW)
	return canvas, true
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
		placeLayer(canvas, placed, termW)
	}
	return canvas
}

// placeLayer writes a layer's Content onto the canvas at its Rect, padding
// each content line to the layer's width and truncating overwidth lines.
func placeLayer(canvas []string, l Layer, termW int) {
	for i, line := range l.Content {
		y := l.Rect.Y + i
		if y < 0 || y >= len(canvas) {
			continue
		}
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

// AgentFrame is a structured, protocol-free representation of the current
// screen for AI agent tooling: it lets an agent "see" the TUI without parsing
// escape codes. Computed from the same Scene the Compositor renders, so agent
// and terminal always agree.
type AgentFrame struct {
	Width, Height int
	Cursor        *CursorPos
	Layers        []AgentLayer // in z-order
	Visible       []string     // ANSI-stripped visible viewport, top-to-bottom reading order
}

// AgentFrame produces the plain-text structured view of the Scene.
// viewportH is the terminal height (number of visible rows).
func (s *Scene) AgentFrame(viewportH int) AgentFrame {
	canvas, _ := s.compose()
	height := len(canvas)
	vTop := height - viewportH
	if vTop < 0 {
		vTop = 0
	}
	vBottom := vTop + viewportH
	if vBottom > height {
		vBottom = height
	}

	frame := AgentFrame{Width: s.TerminalW, Height: viewportH, Cursor: s.Cursor}

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
	clearOnShrink       bool
}

// NewCompositor creates a Compositor bound to a Terminal.
func NewCompositor(term Terminal) *Compositor {
	return &Compositor{terminal: term, clearOnShrink: true}
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

	canvas, hasOverlay := scene.compose()
	// Append per-line resets so SGR attributes / OSC 8 links do not bleed.
	canvas = applyLineResets(canvas)

	fl := c.computeFrameLocals(width, height)
	fullRender := func(clear bool) { c.fullFrame(canvas, scene.Cursor, width, height, clear) }

	if c.earlyFullRenderPath(canvas, hasOverlay, fl.widthChanged, fl.heightChanged, fullRender) {
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

func (c *Compositor) earlyFullRenderPath(canvas []string, hasOverlay, widthChanged, heightChanged bool, fullRender func(bool)) bool {
	if len(c.prevLines) == 0 && !widthChanged && !heightChanged {
		fullRender(false)
		return true
	}
	if widthChanged || heightChanged {
		fullRender(true)
		return true
	}
	if c.clearOnShrink && len(canvas) < c.maxLinesRendered && !hasOverlay {
		fullRender(true)
		return true
	}
	return false
}

func (c *Compositor) renderChangePath(canvas []string, hasOverlay bool, cursor *CursorPos, fl *frameLocals, fullRender func(bool)) {
	firstChanged, lastChanged := firstLastDiff(c.prevLines, canvas)
	appendedLines := len(canvas) > len(c.prevLines)
	if appendedLines {
		if firstChanged == -1 {
			firstChanged = len(c.prevLines)
		}
		lastChanged = len(canvas) - 1
	}

	if firstChanged == -1 {
		c.positionHardwareCursor(cursor, len(canvas))
		c.previousViewportTop = fl.prevViewportTop
		c.prevH = fl.height
		return
	}
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
		&fl.prevViewportTop, &fl.viewportTop, fl.height)

	c.cursorRow = max(0, len(canvas)-1)
	c.hardwareCursorRow = finalCursorRow
	c.viewportTop = fl.viewportTop
	c.maxLinesRendered = max(c.maxLinesRendered, len(canvas))
	c.previousViewportTop = max(fl.prevViewportTop, finalCursorRow-fl.height+1)
	c.positionHardwareCursor(cursor, len(canvas))
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
	buf.WriteString("\x1b[?2026l")
	c.terminal.Write([]byte(buf.String()))

	c.cursorRow = max(0, len(canvas)-1)
	c.hardwareCursorRow = c.cursorRow
	if clear {
		c.maxLinesRendered = len(canvas)
	} else {
		c.maxLinesRendered = max(c.maxLinesRendered, len(canvas))
	}
	bufferLength := max(height, len(canvas))
	c.previousViewportTop = max(0, bufferLength-height)
	c.viewportTop = c.previousViewportTop
	c.positionHardwareCursor(cursor, len(canvas))
	c.prevLines = copySlice(canvas)
	c.prevW = width
	c.prevH = height
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
func (c *Compositor) writeDifferential(canvas []string, firstChanged, lastChanged, width int,
	prevViewportTop, viewportTop *int, height int) int {

	// Bottom-anchor the viewport to the new content tail before the CUP write
	// loop. This is what makes streaming scroll instead of clobber.
	desiredViewportTop := len(canvas) - height
	if desiredViewportTop < 0 {
		desiredViewportTop = 0
	}
	if desiredViewportTop > *prevViewportTop {
		scroll := desiredViewportTop - *prevViewportTop
		var scrollBuf strings.Builder
		scrollBuf.WriteString("\x1b[?2026h")
		scrollBuf.WriteString(fmt.Sprintf("\x1b[%d;1H", height))
		scrollBuf.WriteString(strings.Repeat("\n", scroll))
		scrollBuf.WriteString("\x1b[?2026l")
		c.terminal.Write([]byte(scrollBuf.String()))
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
	if extra := len(c.prevLines) - len(canvas); extra > 0 {
		for k := 0; k < extra; k++ {
			row := clampRow((len(canvas)+k)-vtop+1, height)
			buf.WriteString(fmt.Sprintf("\x1b[%d;1H\x1b[2K", row))
		}
		finalCursorRow = max(0, len(canvas)-1)
	}
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

// positionHardwareCursor moves the terminal cursor to the logical cursor
// position (for IME) using ABSOLUTE CUP, so it is immune to the same auto-wrap
// drift that relative moves suffer.
func (c *Compositor) positionHardwareCursor(cp *CursorPos, totalLines int) {
	if cp == nil || totalLines <= 0 {
		c.terminal.HideCursor()
		return
	}
	targetRow := max(0, min(cp.Row, totalLines-1))
	targetCol := max(0, cp.Col)
	screenRow := clampRow(targetRow-c.viewportTop+1, c.prevH)
	c.terminal.Write([]byte(fmt.Sprintf("\x1b[%d;%dH", screenRow, targetCol+1)))
	c.hardwareCursorRow = targetRow
	c.terminal.ShowCursor()
}

// applyLineResets appends a reset sequence to every non-image line so SGR
// attributes and OSC 8 hyperlinks do not bleed across lines.
func applyLineResets(lines []string) []string {
	const reset = SEGMENT_RESET
	for i, line := range lines {
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
