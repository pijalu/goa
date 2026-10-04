// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package webui

import (
	"strings"
	"sync"
	"sync/atomic"

	"github.com/pijalu/goa/tui"
)

// MaxFrameBytes bounds a single inbound client message. Input and control
// documents are tiny; anything larger is a bug or an attack, never a
// legitimate client.
const MaxFrameBytes = 1 << 20

// pendingInputLimit bounds the keystrokes a VirtualTerminal holds before the
// engine wires its input callback. A tty's own input buffer is finite too, and
// this one is fed by a socket: an unbounded buffer would be a memory-growth
// vector for a client that types into a session that never starts. Bytes past
// the limit are dropped (the overflow is the least valuable part of a burst
// nobody asked the session to interpret yet).
const pendingInputLimit = 4096

// FrameSink receives the frames a VirtualTerminal produces. The Hub is the
// production implementation; tests use a recorder.
type FrameSink interface {
	// Publish hands a frame to the sink. It must never block the caller for
	// long: the render loop calls it synchronously.
	Publish(f *Frame)
	// HasClients reports whether the frame would reach anyone. A screen nobody
	// is watching still has to be tracked, but it does not have to be diffed,
	// run-collapsed and encoded 30 times a second — the sink is the only thing
	// that knows, so it is the only thing that can say.
	HasClients() bool
}

// VirtualTerminal implements tui.Terminal on top of a CellGrid. The whole TUI
// engine runs against it unchanged: the same Compositor computes the same
// differential byte stream, the bytes land in a virtual screen instead of a
// TTY, and the resulting cell rows are published as frames.
//
// It deliberately never touches the process terminal: no raw mode, no screen
// ownership, no writes to stdout. A browser-reachable session must not fight
// the terminal it was launched from.
type VirtualTerminal struct {
	grid *CellGrid
	sink FrameSink

	mu       sync.RWMutex
	onInput  func(string)
	onResize func()
	started  bool
	stopped  bool
	// pendingInput holds keystrokes delivered before Start wired onInput.
	// Guarded by mu.
	pendingInput strings.Builder

	frameNo atomic.Uint64
	codec   FrameCodec
}

var _ tui.Terminal = (*VirtualTerminal)(nil)

// NewVirtualTerminal creates a virtual screen of cols×rows with no sink
// attached (set one with SetSink before Start, or pass it to NewServer).
func NewVirtualTerminal(cols, rows int) *VirtualTerminal {
	if cols <= 0 {
		cols = DefaultCols
	}
	if rows <= 0 {
		rows = DefaultRows
	}
	vt := &VirtualTerminal{grid: NewCellGrid(cols, rows)}
	// Revision 1 is the initial screen. Starting at 1 rather than 0 keeps "the
	// client holds nothing" (since=0) distinguishable from "the client holds the
	// first screen it was ever sent" (since=1), which is what makes a resume
	// answerable.
	vt.frameNo.Store(1)
	return vt
}

// Grid exposes the authoritative cell grid (the /text mirror and tests read it
// directly).
func (v *VirtualTerminal) Grid() *CellGrid { return v.grid }

// SetSink attaches (or detaches, with nil) the frame sink.
func (v *VirtualTerminal) SetSink(s FrameSink) {
	v.mu.Lock()
	v.sink = s
	v.mu.Unlock()
}

// Start stores the engine's input and resize callbacks. Unlike
// ProcessTerminal it acquires no raw mode and claims no screen: there is no
// TTY behind it.
//
// Keystrokes that arrived first are replayed through the callback now, in
// order: a page can attach and type as soon as the listener is up, which is a
// few milliseconds before the session's terminal exists, and those bytes must
// not vanish (a tty holds typed bytes until a reader arrives; so does this).
func (v *VirtualTerminal) Start(onInput func(string), onResize func()) {
	v.mu.Lock()
	v.onInput, v.onResize = onInput, onResize
	v.started = true
	v.stopped = false
	pending := v.pendingInput.String()
	v.pendingInput.Reset()
	v.mu.Unlock()

	if pending != "" {
		v.dispatch(onInput, pending)
	}
}

// Stop detaches from the sink. The grid is left intact so a late reader can
// still inspect the final screen.
func (v *VirtualTerminal) Stop() {
	v.mu.Lock()
	if v.stopped {
		v.mu.Unlock()
		return
	}
	v.stopped = true
	v.onInput, v.onResize = nil, nil
	// The session is over: keystrokes typed into it now have no engine to
	// reach, and must not be replayed into a future session either.
	v.pendingInput.Reset()
	v.mu.Unlock()
	v.SetSink(nil)
}

// Write feeds compositor output into the grid and publishes a frame when the
// screen actually moved.
func (v *VirtualTerminal) Write(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	v.WriteString(string(p))
	return len(p), nil
}

// WriteString is Write for a string.
func (v *VirtualTerminal) WriteString(s string) {
	if s == "" {
		return
	}
	if !v.grid.Process(s) {
		return
	}
	v.publish(false)
	// The first write that changed the screen after a geometry change IS the
	// repaint that re-anchors it: its own scroll overflow (rows of the screen
	// being replaced) has just been dropped, so the suppression ends here
	// (bugs.md B3).
	v.grid.EndGeometryChange()
}

// Size reports the virtual screen geometry.
func (v *VirtualTerminal) Size() (width, height int) { return v.grid.Size() }

// SetRaw is a no-op: there is no terminal to put in raw mode. The returned
// restore func is safe to call.
func (v *VirtualTerminal) SetRaw() (func(), error) { return func() {}, nil }

// HideCursor hides the caret carried by subsequent frames.
func (v *VirtualTerminal) HideCursor() {
	if v.grid.Cursor().Visible {
		v.grid.SetCursorVisible(false)
		v.publish(true)
	}
}

// ShowCursor shows the caret carried by subsequent frames.
func (v *VirtualTerminal) ShowCursor() {
	if !v.grid.Cursor().Visible {
		v.grid.SetCursorVisible(true)
		v.publish(true)
	}
}

// ClearScreen blanks the grid and publishes a full repaint.
func (v *VirtualTerminal) ClearScreen() {
	v.grid.Clear()
	v.publish(true)
}

// SetTitle records the window title; it rides the next frame so the browser
// can set document.title.
func (v *VirtualTerminal) SetTitle(title string) {
	if v.grid.Title() == title {
		return
	}
	v.grid.SetTitle(title)
	v.publish(false)
}

// Resize changes the virtual screen geometry and fires the engine's resize
// callback — the same signal SIGWINCH delivers for a real terminal, so the
// engine's geometry-reset full-repaint path is reused unchanged.
func (v *VirtualTerminal) Resize(cols, rows int) {
	if !v.resizeChanged(cols, rows) {
		return
	}
	v.grid.Resize(cols, rows)
	v.publish(true)
	v.mu.RLock()
	cb := v.onResize
	v.mu.RUnlock()
	if cb != nil {
		cb()
	}
}

// resizeChanged reports whether the requested geometry differs from the
// current one. Out-of-range values are still "changed" — the grid clamps them
// and the first frame already carries the clamped size.
func (v *VirtualTerminal) resizeChanged(cols, rows int) bool {
	cc, cr := v.grid.Size()
	return cc != cols || cr != rows
}

// Input delivers raw terminal bytes from a transport to the engine, exactly as
// a real keyboard read would. Panics from the engine's handler are contained:
// one bad key must not kill the transport goroutine.
//
// Before the engine has started, the bytes are held (bounded) instead of
// dropped and replayed by Start: the listener accepts clients the moment it
// binds, a few milliseconds before the session wires its input callback, and a
// page that connects in that window must not lose what the user typed.
func (v *VirtualTerminal) Input(s string) {
	if s == "" {
		return
	}

	v.mu.Lock()
	cb := v.onInput
	if cb == nil {
		// Not started yet: hold the bytes for Start. A stopped terminal holds
		// nothing — there is no session left to type into.
		if !v.stopped && v.pendingInput.Len() < pendingInputLimit {
			room := pendingInputLimit - v.pendingInput.Len()
			if len(s) > room {
				s = s[:room]
			}
			v.pendingInput.WriteString(s)
		}
		v.mu.Unlock()
		return
	}
	v.mu.Unlock()

	v.dispatch(cb, s)
}

func (v *VirtualTerminal) dispatch(cb func(string), s string) {
	defer func() { _ = recover() }()
	cb(s)
}

// publish builds and ships a frame. full forces every row into the patch set
// (used after a geometry change or a screen clear).
//
// With nobody attached the frame is not built at all: the dirty marks are
// dropped and the grid keeps tracking its screen, so the next client to attach
// gets an authoritative full snapshot (which is what an attach does anyway)
// instead of the server spending a diff, a run collapse and a JSON encode per
// engine frame on a screen no one can see.
func (v *VirtualTerminal) publish(full bool) {
	v.mu.RLock()
	sink := v.sink
	v.mu.RUnlock()
	if sink == nil {
		return
	}
	if !sink.HasClients() {
		v.grid.DiscardChanges()
		return
	}
	seq := v.frameNo.Add(1)
	var patches []RowPatch
	if full {
		patches = v.grid.FullPatches()
	} else {
		patches = v.grid.Patches()
	}
	frame := NewFrame(seq, v.grid, patches, v.grid.Title(), full)
	// Rows that scrolled off since the last publish ride this frame: the
	// transport emits them as a separate "scrollback" message so the client can
	// keep a transcript without disturbing the live grid.
	frame.Scrollback = v.grid.TakeScrollback()
	sink.Publish(frame)
}

// PublishFull ships a full-snapshot frame — what a newly attached client gets
// as its first frame.
func (v *VirtualTerminal) PublishFull() {
	v.publish(true)
}

// FullFrame builds (without publishing) a frame carrying the whole current
// screen — what a joining, reconnecting or resyncing client receives.
//
// It reports the grid's *current* revision rather than consuming a new one: a
// snapshot describes the state a client already holding Seq() has, so bumping
// the counter here would make "you are up to date" unanswerable and force every
// attach to look like a catch-up. Only an actual publish advances the revision.
func (v *VirtualTerminal) FullFrame() *Frame {
	f := NewFrame(v.Seq(), v.grid, v.grid.FullPatches(), v.grid.Title(), true)
	// A joining client gets the transcript it missed in the same message set.
	f.Scrollback = v.grid.TakeScrollback()
	return f
}

// Resync decides what a client that announced the revision it holds must be
// sent. A client whose seq matches the grid is already showing the truth and is
// sent nothing; anything else — behind, or ahead of a grid that was reset under
// it — is sent the authoritative screen. The bool reports whether a frame is
// due, so a transport can answer "you are current" by staying quiet.
func (v *VirtualTerminal) Resync(since uint64) (*Frame, bool) {
	if since != 0 && since == v.Seq() {
		return nil, false
	}
	return v.FullFrame(), true
}

// Seq reports the last frame number published.
func (v *VirtualTerminal) Seq() uint64 { return v.frameNo.Load() }

// EncodeFrame exposes the codec used for frames, so the transports share one
// wire format.
func (v *VirtualTerminal) EncodeFrame(f *Frame) ([]byte, error) { return v.codec.EncodeFrame(f) }
