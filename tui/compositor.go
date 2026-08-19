// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"strings"
	"sync"
)

// Rect is a logical rectangle in the virtual buffer (0-indexed).

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

	// clearGen is the clear generation: Clear() increments it and Render drops
	// any scene stamped with an older generation (Scene.ClearGen). A scene
	// built BEFORE a Clear but rendered after it would repaint the old canvas,
	// consume clearRequested, and restore the stale scrollback watermark —
	// leaving a blank screen with the cursor clamped to row 1 until a resize
	// (the reported /new bugs). Dropping it keeps the wipe pending for the
	// next, fresh frame; Clear always requests one.
	clearGen uint64

	// lastScrollCount is the number of rows the current frame's scroll advance
	// (emitSteadyScroll) wrote at the bottom of the transcript region. It is set
	// immediately before repaintWindow runs and consumed only by it, then reset
	// to 0 for the next frame. repaintWindow skips those already-written bottom
	// rows so a scrolling frame never emits a row twice.
	lastScrollCount int

	// scrollbackDirty reports that the terminal scrollback diverged from the
	// canvas: a mid-transcript growth above the visible window (a streaming
	// block growing above later-appended content, e.g. /goal:list or /quota
	// landing mid-stream) advanced the watermark without emitting the grown
	// rows, because a terminal scrollback cannot insert rows in the middle.
	// The screen stays correct (the growth is above the window), so the
	// divergence is invisible until the user scrolls up; a single full reset
	// when the conversation settles (Scene.MutationGen unchanged) re-syncs the
	// scrollback. This replaces the previous per-chunk full reset, which
	// re-emitted the ENTIRE transcript on every stream chunk — O(transcript)
	// per chunk (uniseg width truncation dominates: the CPU >100% storm on
	// long sessions) that also yanked the terminal viewport with repeated
	// \x1b[3J scrollback wipes (the reported jump-back / scroll-back-down
	// during /goal:list while streaming).
	scrollbackDirty bool
	// prevMutationGen is the Scene.MutationGen of the previous rendered frame.
	// When scrollbackDirty and the mutation gen is unchanged, the conversation
	// has settled and the deferred scrollback sync may run exactly once.
	prevMutationGen uint64

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
//
// It runs on the caller's goroutine (TUI.Start), NOT the renderLoop, and
// writes to the shared terminal — so it must take c.mu like every other
// Compositor method. Without the lock a concurrent shutdown (Stop→Restore,
// which holds mu) or an in-flight frame could interleave terminal writes with
// this clear, corrupting the output stream and tripping the race detector
// (bugs.md: TestRunWizardWithTerminal_FirstFrameRenders race).
func (c *Compositor) InitialClear() {
	c.mu.Lock()
	defer c.mu.Unlock()
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
	c.scrollbackDirty = false // the wipe re-syncs scrollback to the new canvas
	c.prevMutationGen = 0
	c.clearGen++
	c.clearRequested = true
}

// ClearGen reports the current clear generation. Snapshot builders stamp it
// into Scene.ClearGen; see the clearGen field docs for the stale-scene drop.
func (c *Compositor) ClearGen() uint64 {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.clearGen
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

	// A scene older than the last Clear predates the wipe: rendering it would
	// repaint stale content and swallow clearRequested (the /new race where the
	// renderLoop holds a pre-clear snapshot across a session reset). Drop it —
	// Clear's render request produces a fresh, correctly-wiped frame next.
	if scene.ClearGen != c.clearGen {
		return
	}

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
	// streaming block growing ABOVE later-appended content, e.g. /quota or a
	// screen-filling /goal:list landing mid-stream), the incremental scroll
	// emission would scroll wrong row identities into scrollback and skip the
	// real ones. See handleMidTranscriptEdit for the defer-and-sync strategy
	// that replaced the per-chunk full reset (the CPU storm).
	if c.handleMidTranscriptEdit(scene, canvas, width, height, kind, clearPending) {
		return
	}

	switch kind {
	case frameFirst:
		// First frame: InitialClear already wiped screen+scrollback; drawWindow
		// emits any off-screen rows into scrollback then draws the window. When
		// this first frame follows a Clear() the wipe has NOT happened yet, so
		// drawWindow performs it atomically inside the frame's sync.
		c.drawWindow(canvas, scene.Cursor, width, height, clearPending)
		c.scrollbackDirty = false
	case frameGeometryReset:
		// Width change: the terminal's scrollback no longer corresponds to
		// the canvas layout (wrap reflowed), so the incremental diff cannot
		// map it. Reset scrollback and re-emit every off-screen row at the
		// current geometry, then repaint the window. The re-emit also
		// re-syncs any deferred scrollback divergence.
		c.drawWindowResetScrollback(canvas, scene.Cursor, width, height)
		c.scrollbackDirty = false
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
	c.prevMutationGen = scene.MutationGen
}

// frameKind classifies a frame so Render dispatches on a single value. The
// geometry (terminal size and bottom-chrome height) determines whether the
// terminal's scrollback still maps to the canvas: a geometry change requires
// a scrollback reset, anything else uses the incremental diff.
type frameKind int
