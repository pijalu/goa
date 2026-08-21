// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// CURSOR_MARKER is a zero-width escape sequence emitted by focused components.
const CURSOR_MARKER = "\x1b_pi:c\x07"

// SEGMENT_RESET resets all SGR attributes and closes any open OSC 8 hyperlink.
const SEGMENT_RESET = "\x1b[0m\x1b]8;;\x07"

// recoverToLog recovers from panics in TUI loops and logs the stack to stderr
// so a single malformed component or callback cannot kill the whole session.
func recoverToLog(where string) {
	if r := recover(); r != nil {
		fmt.Fprintf(os.Stderr, "goa: panic recovered in %s: %v\n", where, r)
	}
}

// OverlayOptions define positioning and sizing for overlay components.
type OverlayOptions struct {
	Width        int  // overlay width (0 = auto)
	Height       int  // overlay height (0 = auto)
	Center       bool // center in terminal
	BottomOffset int  // offset from bottom (0 = at bottom)
	CaptureInput bool // if true, overlay receives keyboard input
}

// OverlayHandle controls a shown overlay.
type OverlayHandle struct {
	Hide func()
	// SetCaptureInput enables or disables input capture for this overlay.
	// When capture is disabled, input is routed to the previously-focused
	// component while the overlay remains visible.
	SetCaptureInput func(capture bool)
	// IsVisible reports whether the overlay is still on screen. Hosts should
	// consult this after invoking a callback that may have closed the overlay
	// (e.g. a confirm that submits and dismisses the review pager) before
	// restoring capture/title — otherwise input gets routed to a hidden
	// component and the app appears frozen.
	IsVisible func() bool
}

// TUI is the main terminal UI engine with component tree, differential rendering,
// input routing, and overlay system. The architecture: content is
// written sequentially into the terminal's scrollback via \r\n newlines,
// so the terminal's native scrollbar works for history navigation.
type TUI struct {
	children []Component
	terminal Terminal

	// compositor owns ALL terminal-protocol state and output. The TUI never
	// touches escape sequences, cursor math, or the diff baseline directly —
	// it assembles a protocol-free Scene (layers + cursor) and hands it to the
	// compositor owns terminal-protocol output; TUI never touches escapes directly.
	compositor *Compositor

	// termW/termH are an atomic size cache published at the end of each render
	// so cross-goroutine readers (e.g. Editor.pageScroll on the input
	// goroutine) observe the size without taking mu (which would self-deadlock
	// because TerminalRows is also called from inside render).
	termW     atomic.Int64
	termH     atomic.Int64
	stopped   atomic.Bool
	closeDone sync.Once
	stopOnce  sync.Once // guards the full synchronous shutdown sequence
	started   atomic.Bool

	// replaySuppressed pauses frame rendering while a scrollback ReplayRunner
	// owns the terminal (plan T3). Written by the command loop inside the
	// switch Apply, read by the render loop.
	replaySuppressed atomic.Bool

	focus        *FocusStack
	// overlayMu guards overlayStack: mutation happens on the commandLoop
	// (or inline via Apply pre-RunLoops), while buildOverlayLayers reads it
	// from the render path; the two can overlap when Apply runs inline.
	overlayMu    sync.RWMutex
	overlayStack []*overlayEntry

	// Actor model: the commandLoop is the SOLE owner
	// of mutable state; renderLoop is the SOLE terminal outputter; they
	// communicate via the immutable Scene snapshot on snapReq/`latest`. When the
	// loops are not running (tests, pre-RunLoops), Apply runs inline so tests
	// stay single-goroutine and need no locks.
	cmds          chan func()        // commandLoop inbox
	snapReq       chan chan<- *Scene // renderLoop requests a snapshot from commandLoop
	loopsRunning  atomic.Bool
	loopGoroutine atomic.Uint64 // commandLoop's goroutine ID; lets ApplySync detect re-entrancy

	// dirtyChan signals the renderLoop that a new frame is needed. The channel
	// is buffered so that only one pending signal is kept; the renderLoop
	// throttles to a maximum of 60fps.
	dirtyChan chan struct{}

	// Async render scheduling
	done chan struct{}

	// keyLog is the optional asynchronous keystroke tracer. It is nil unless
	// explicitly enabled via config (logging.trace_keys / GOA_LOGGING_TRACEKEYS)
	// or the --debug-keys flag.
	keyLog *keyLogger

	// OnDeleteLast is called when Ctrl+Backspace is pressed.
	// Used to delete the last completed chat message.
	OnDeleteLast func()

	// OnToggleGoalBubble is called when Ctrl+G is pressed.
	OnToggleGoalBubble func()

	// OnEditSteering is called when the steering-edit key (Alt+E) is pressed.
	// The host moves any pending steering message back into the input line
	// for editing and empties the steering queue.
	OnEditSteering func()

	// OnCycleThinkingLevel is called when Shift+Tab is pressed.
	OnCycleThinkingLevel func()

	// OnChangeMode is called when the major-mode cycle key is pressed.
	OnChangeMode func()

	// OnOpenModeSelector is called when the mode-selector key is pressed.
	OnOpenModeSelector func()

	// OnCycleAutonomy is called when the autonomy-cycle key is pressed.
	OnCycleAutonomy func()

	// OnChangeModel is called when the model-change key is pressed.
	OnChangeModel func()

	// OnToggleThinkingBlocks is called when the thinking-blocks toggle key is pressed.
	OnToggleThinkingBlocks func()

	// OnOpenAgentTabs opens the tab picker overlay for the persistent multi-agent
	// run view (Ctrl+x). The picker lists tabs as a numbered menu supporting
	// number-jump, arrows, enter, esc. The key is a layout-independent control
	// char (safe under goa's raw terminal) and the callback name is source-
	// agnostic so pipeline/swarm reuse it later.
	OnOpenAgentTabs func()

	// OnCancelInputRequest is called when Ctrl+C is pressed while the editor
	// is empty and a main-input request is active. It lets the host cancel
	// the pending prompt instead of quitting. If it returns true, the quit is
	// suppressed.
	OnCancelInputRequest func() bool

	// pluginHotkeys holds JS-plugin-registered keyboard shortcuts, checked
	// after built-in appShortcuts. Guarded by pluginHotkeysMu because plugins
	// register from the plugin runner goroutine while the TUI reads on the
	// input loop.
	pluginHotkeys   []pluginHotkey
	pluginHotkeysMu sync.RWMutex
}

type overlayEntry struct {
	comp Component
	opts OverlayOptions
}

// NewTUI creates a TUI engine with a Compositor bound to the terminal.
func NewTUI(term Terminal) *TUI {
	return &TUI{
		terminal:   term,
		compositor: NewCompositor(term),
		done:       make(chan struct{}),
	}
}

// SetTitle sets the terminal window title via the Terminal interface.
func (t *TUI) SetTitle(title string) {
	t.terminal.SetTitle(title)
}

// AddChild adds a component to the tree.
func (t *TUI) AddChild(c Component) { t.children = append(t.children, c) }

// ReplaceChild swaps old for new at the SAME position in the component tree,
// keeping the stacked layout (header → transcript → … → input → footer)
// stable across a view switch. It returns false when old is not a child.
// Command-loop only: like AddChild, the children slice is owned by the
// commandLoop; callers route the swap through Apply.
func (t *TUI) ReplaceChild(old, new Component) bool {
	for i, ch := range t.children {
		if ch == old {
			t.children[i] = new
			return true
		}
	}
	return false
}

// SetFocus sets the focused component via the FocusStack. The first call
// establishes the base focus; subsequent calls Replace the current top (used
// by the host to restore the main editor, and by overlay capture toggles).
func (t *TUI) SetFocus(c Component) {
	if t.focus == nil {
		t.focus = NewFocusStack(c)
	} else {
		t.focus.Replace(c)
	}
	if f, ok := c.(Focusable); ok {
		f.SetFocused(true)
	}
}

// Focused returns the component that currently receives input (FocusStack top).
func (t *TUI) Focused() Component {
	if t.focus == nil {
		return nil
	}
	return t.focus.Top()
}

// TerminalRows returns the current terminal height in rows.
// Safe to call from any goroutine. Reads an atomic snapshot of the size
// published by the render loop; never takes mu to avoid self-deadlock.
func (t *TUI) TerminalRows() int {
	if h := t.termH.Load(); h > 0 {
		return int(h)
	}
	_, h := t.compositor.PrevSize()
	return h
}

// TerminalCols returns the current terminal width in columns.
func (t *TUI) TerminalCols() int {
	if w := t.termW.Load(); w > 0 {
		return int(w)
	}
	w, _ := t.compositor.PrevSize()
	return w
}

// publishSize caches the rendered size in the atomic fields so cross-goroutine
// readers (TerminalRows/TerminalCols) see a consistent value without taking
// mu. Caller must hold mu.
func (t *TUI) publishSize(w, h int) {
	t.termW.Store(int64(w))
	t.termH.Store(int64(h))
}

// Buffer returns a copy of the previous frame's composed canvas.
func (t *TUI) Buffer() []string {
	return t.compositor.Buffer()
}

// AgentFrame returns a structured, ANSI-free view of the current screen for
// AI tooling (AgentView). It is computed
// from the same Scene the Compositor renders, so agent and terminal agree.
//
// The scene is built on the commandLoop (via ApplySync) so component state is
// read by the sole owner — no locking, consistent with the Actor model.
// FullRedrawCount exposes the compositor's count of full-screen redraws,
// for diagnostics/tests asserting that streaming/edits do not trigger
// excessive full wipes (Bug 2).
func (t *TUI) FullRedrawCount() int {
	return t.compositor.FullRedrawCount()
}

// SetRenderTrace enables per-frame compositor tracing to path (JSONL). It is
// the entry point for config Logging.render_trace / --render-log /
// GOA_LOGGING_RENDER_TRACE, exposing byte-level rendering diagnosis from the
// CLI. The filmstrip/AgentFrame cannot see compositor-emission bugs by
// design; this trace can.
func (t *TUI) SetRenderTrace(path string) error {
	return t.compositor.EnableRenderTrace(path)
}

func (t *TUI) AgentFrame() AgentFrame {
	var frame AgentFrame
	t.ApplySync(func() {
		w, h := t.terminal.Size()
		scene := t.buildScene(w, h)
		frame = scene.AgentFrame(h)
	})
	return frame
}

// VisibleText returns the current visible screen as a single ANSI-free string
// in reading order (top-to-bottom), with the cursor marker shown as '▏'. This
// is the most convenient "screenshot to text" entry point for AI agent tooling
// that needs to see what the TUI currently shows without parsing escape codes.
func (t *TUI) VisibleText() string {
	frame := t.AgentFrame()
	var b strings.Builder
	for _, line := range frame.Visible {
		b.WriteString(line)
		b.WriteByte('\n')
	}
	return b.String()
}

// ShowOverlay shows a modal component on top of the content. When CaptureInput
// is set, the overlay is pushed onto the FocusStack so it receives input until
// hidden; hiding pops it and restores the previous focus exactly.
//
// The overlay registration runs on the commandLoop via ApplySync (the loop is
// the sole state owner). The returned OverlayHandle is valid once ShowOverlay
// returns. Its Hide/SetCaptureInput closures likewise route through Apply, so
// they are safe to call from any goroutine.
func (t *TUI) ShowOverlay(comp Component, opts OverlayOptions) *OverlayHandle {
	entry := &overlayEntry{comp: comp, opts: opts}
	t.ApplySync(func() { t.addOverlayLocked(entry, comp, opts) })
	visible := true
	return &OverlayHandle{
		Hide: func() {
			visible = false
			t.Apply(func() { t.hideOverlay(entry) })
		},
		SetCaptureInput: func(capture bool) {
			t.Apply(func() { t.setOverlayCapture(entry, comp, capture) })
		},
		IsVisible: func() bool { return visible },
	}
}

// addOverlayLocked appends an overlay entry and (optionally) pushes it onto
// the FocusStack. Runs on the commandLoop; the "Locked" suffix denotes
// loop-ownership, not a mutex.
func (t *TUI) addOverlayLocked(entry *overlayEntry, comp Component, opts OverlayOptions) {
	t.overlayMu.Lock()
	t.overlayStack = append(t.overlayStack, entry)
	t.overlayMu.Unlock()
	if opts.CaptureInput {
		if t.focus == nil {
			t.focus = NewFocusStack(comp)
		} else {
			t.focus.Push(comp)
		}
		if f, ok := comp.(Focusable); ok {
			f.SetFocused(true)
		}
	}
	t.RequestRender()
}

// hideOverlay removes an overlay entry and restores the previous focus.
// Runs on the commandLoop (sole state owner).
func (t *TUI) hideOverlay(entry *overlayEntry) {
	t.overlayMu.Lock()
	for i, e := range t.overlayStack {
		if e == entry {
			t.overlayStack = append(t.overlayStack[:i], t.overlayStack[i+1:]...)
			break
		}
	}
	t.overlayMu.Unlock()
	if t.focus != nil {
		if prev := t.focus.Pop(entry.comp); prev != nil {
			if f, ok := prev.(Focusable); ok {
				f.SetFocused(true)
			}
		}
	}
	t.RequestRender()
}

// setOverlayCapture toggles input capture for an overlay, pushing/popping it
// on the FocusStack accordingly. Runs on the commandLoop (sole state owner).
func (t *TUI) setOverlayCapture(entry *overlayEntry, comp Component, capture bool) {
	entry.opts.CaptureInput = capture
	if t.focus != nil {
		if capture {
			t.focus.Push(comp)
		} else {
			t.focus.Pop(comp)
		}
	}
	t.RequestRender()
}

// Start enters raw mode, sizes the terminal, and renders the first frame.
// It does NOT launch the command/render loops — call RunLoops() for that
// (production). Tests call Start() only, so they stay single-goroutine and
// can mutate components directly without locks (single-ownership via commandLoop).
func (t *TUI) Start() error {
	t.done = make(chan struct{})
	t.started.Store(true)

	t.terminal.Start(func(data string) { t.Apply(func() { t.handleKey(data) }) }, func() { t.RequestRender() })
	t.terminal.HideCursor()

	w, h := t.terminal.Size()
	t.termW.Store(int64(w))
	t.termH.Store(int64(h))

	// Full screen clear then render first content (inline; loops not running).
	t.compositor.InitialClear()
	return nil
}

// RunLoops launches the commandLoop (sole state owner) and renderLoop (sole
// terminal outputter) — the Actor model. Production calls this after Start();
// tests do not, keeping them single-goroutine. After RunLoops, ALL state
// mutations must go through Apply.
//
// Channel initialization happens BEFORE loopsRunning is set to true. This
// ordering is critical: Apply checks loopsRunning to decide whether to send
// on t.cmds or run inline. If loopsRunning were set first (as a naive Swap
// would do), Apply could observe loopsRunning=true while t.cmds is still nil,
// sending on a nil channel and blocking forever. By creating the channels
// first and using CompareAndSwap, the happens-before chain guarantees Apply
// always sees a fully-initialized engine when it observes loopsRunning=true.
func (t *TUI) RunLoops() {
	if t.loopsRunning.Load() {
		return // already running
	}
	t.cmds = make(chan func(), 256)
	t.snapReq = make(chan chan<- *Scene)
	t.dirtyChan = make(chan struct{}, 1)
	if !t.loopsRunning.CompareAndSwap(false, true) {
		return // another caller won the race
	}
	go t.commandLoop()
	go t.renderLoop()
	go t.listenResize()
}

// LoopsRunning reports whether the Actor-model command/render loops are active.
// Components that schedule asynchronous work on the commandLoop can use this
// to avoid creating goroutines that would run inline (and race) in the
// single-goroutine test mode.
func (t *TUI) LoopsRunning() bool { return t.loopsRunning.Load() }

// commandLoop is the SOLE goroutine that mutates component state. It processes
// Commands from cmds and builds Scene snapshots on demand for the renderLoop.
// Single ownership is what lets components drop their mutexes.
func (t *TUI) commandLoop() {
	t.loopGoroutine.Store(goroutineID())
	for {
		select {
		case cmd := <-t.cmds:
			t.applyCommand(cmd)
		case reply := <-t.snapReq:
			func() {
				defer recoverToLog("snapshot")
				reply <- t.buildSnapshot()
			}()
		case <-t.done:
			return
		}
	}
}

// applyCommand runs one command on the commandLoop. It takes NO lock: the
// commandLoop is the sole owner of mutable TUI state, so command dispatch
// itself needs no synchronization (serialized by the commandLoop). Commands run
// to completion before the next command begins.
func (t *TUI) applyCommand(cmd func()) {
	defer recoverToLog("command")
	cmd()
	t.RequestRender()
}

// renderLoop is the SOLE terminal outputter. It waits for render requests
// and, when one arrives, requests an immutable Scene snapshot from the
// commandLoop and hands it to the Compositor. A 16ms throttle ensures the
// terminal is updated at most 60 times per second (a ceiling, not a target),
// so bursty state changes coalesce into a single frame.
//
// When at least one tool widget is running, a 100ms periodic ticker fires
// alongside dirtyChan so the elapsed-time display in tool widgets updates
// smoothly (~10fps) even when no streaming events arrive. Without this,
// the elapsed time freezes between events (B002).
func (t *TUI) renderLoop() {
	live := newLiveRenderTicker()
	defer live.stop()

	for {
		select {
		case <-t.dirtyChan:
			if t.stopped.Load() {
				return
			}
			t.renderOneFrame()
			live.sync(t.findChatViewport())
			if !t.throttle() {
				return
			}
		case <-live.tick:
			if t.handleLiveTick(live) {
				return
			}
		case <-t.done:
			return
		}
	}
}

// handleLiveTick refreshes running tool widgets on the 100ms live ticker.
// Returns true when the loop should exit (TUI stopped). Extracted from
// renderLoop to keep its complexity within budget.
func (t *TUI) handleLiveTick(live *liveRenderTicker) bool {
	if t.stopped.Load() {
		return true
	}
	cv := t.findChatViewport()
	if cv == nil || !cv.HasRunningToolWidgets() {
		live.stop()
		return false
	}
	cv.InvalidateRunningToolWidgets()
	t.renderOneFrame()
	return false
}

// renderOneFrame requests a snapshot from the commandLoop and hands it to
// the compositor. Extracted from renderLoop to keep complexity in budget.
func (t *TUI) renderOneFrame() {
	if t.replaySuppressed.Load() {
		return // ReplayRunner owns the terminal; frames resume on replay end (T3)
	}
	reply := make(chan *Scene, 1)
	t.snapReq <- reply
	scene := <-reply
	func() {
		defer recoverToLog("render")
		t.compositor.Render(scene)
		t.publishScrollWatermark()
	}()
}

// throttle sleeps ~16ms to cap the frame rate at ~60fps. Returns false if
// the done channel fired during the wait (loop should exit).
func (t *TUI) throttle() bool {
	select {
	case <-time.After(16 * time.Millisecond):
		return true
	case <-t.done:
		return false
	}
}

// liveRenderTicker manages a 100ms periodic ticker that fires when running
// tool widgets need their elapsed-time display refreshed.
type liveRenderTicker struct {
	ticker *time.Ticker
	tick   <-chan time.Time
}

func newLiveRenderTicker() *liveRenderTicker { return &liveRenderTicker{} }

// sync starts the ticker if the viewport has running tools, stops it otherwise.
func (l *liveRenderTicker) sync(cv *ChatViewport) {
	if cv != nil && cv.HasRunningToolWidgets() {
		l.start()
	} else {
		l.stop()
	}
}

func (l *liveRenderTicker) start() {
	if l.ticker == nil {
		l.ticker = time.NewTicker(100 * time.Millisecond)
		l.tick = l.ticker.C
	}
}

func (l *liveRenderTicker) stop() {
	if l.ticker != nil {
		l.ticker.Stop()
		l.ticker = nil
		l.tick = nil
	}
}

// buildSnapshot builds a Scene from the current component state. Runs on the
// commandLoop (sole state owner), so it takes no lock — every mutation and
// every read of component state is serialized by the loop.
func (t *TUI) buildSnapshot() *Scene {
	w, h := t.terminal.Size()
	scene := t.buildScene(w, h)
	t.publishSize(scene.TerminalW, scene.TerminalH)
	// Stamp the clear generation so the compositor can drop this snapshot if a
	// Clear() (e.g. /new) lands before it is rendered (the stale-scene race).
	scene.ClearGen = t.compositor.ClearGen()
	// Stamp the chat viewport's mutation generation so the compositor can
	// detect when the conversation has settled (no mutation since the last
	// frame) and re-sync a deferred scrollback exactly once.
	if cv := t.findChatViewport(); cv != nil {
		scene.MutationGen = uint64(cv.Generation())
	}
	return scene
}

// Apply submits a Command to the commandLoop. When the loops are not running
// (tests / pre-RunLoops) it runs inline, keeping tests single-goroutine. All
// production state mutations MUST go through Apply so the commandLoop stays
// the sole owner (commandLoop).
func (t *TUI) Apply(cmd func()) {
	if t.loopsRunning.Load() {
		t.cmds <- cmd
		return
	}
	cmd()
}

// ApplySync submits a Command to the commandLoop and blocks until the loop has
// run it. Use it for the rare host call that must observe the effect before
// returning (e.g. ShowOverlay, which hands back an OverlayHandle whose Hide
// closure is only valid once the overlay is registered on the loop).
//
// Re-entrancy: if ApplySync is invoked from the commandLoop itself (a Command
// that triggers an overlay, such as a shortcut callback calling ShowSelector),
// enqueuing would self-deadlock. The loopGoroutine guard detects this and runs
// the Command inline on the loop — preserving single-ownership without
// deadlock.
func (t *TUI) ApplySync(cmd func()) {
	if !t.loopsRunning.Load() {
		cmd()
		return
	}
	if t.loopGoroutine.Load() == goroutineID() {
		cmd()
		return
	}
	done := make(chan struct{})
	t.cmds <- func() {
		cmd()
		close(done)
	}
	<-done
}

// RequestRender flags the renderLoop that state changed and a new frame is
// due. Safe from any goroutine (atomic/channel). The channel is buffered so a
// burst of requests collapses into a single pending signal.
func (t *TUI) RequestRender() {
	if ch := t.dirtyChan; ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// ClearTranscript resets the compositor for a deliberate transcript reset
// (/new, session switch): it wipes the screen and terminal scrollback and
// zeroes the scrollback watermark so the next frame renders the fresh canvas
// as a first frame. Call this when the conversation is intentionally cleared;
// transient mid-stream collapses are handled internally by the watermark clamp
// and must NOT call this.
func (t *TUI) ClearTranscript() {
	t.compositor.Clear()
	t.RequestRender()
}

// ExportFrame captures the compositor's live per-view baseline (previous
// frame rows, scrollback watermark, viewport top) so the currently-mounted
// view can be detached on a view switch. See Compositor.ExportFrame.
func (t *TUI) ExportFrame() FrameState { return t.compositor.ExportFrame() }

// RestoreFrame installs a detached view's saved baseline and requests a full
// visible-window repaint of it (the T2 view-switch behavior: in-place window
// repaint, no scrollback replay). See Compositor.RestoreFrame.
func (t *TUI) RestoreFrame(s FrameState) {
	t.compositor.RestoreFrame(s)
	t.RequestRender()
}

// Compositor exposes the engine's compositor so the app layer can hand it to
// the scrollback ReplayRunner (plan T3). The compositor serializes all its
// own writes; callers must not invoke Render directly.
func (t *TUI) Compositor() *Compositor { return t.compositor }

// SetReplaySuppressed pauses frame rendering while a scrollback replay runs
// (plan T3). While suppressed, renderOneFrame drops the render request: the
// ReplayRunner owns the terminal's scroll region and the two must never write
// concurrently. The replay's completion handler (on the command loop) clears
// the suppression and requests the resume frame. Without suppression a frame
// between switch and replay-end would emit the same backlog rows the runner
// is emitting — the duplicate-scroll bug this prevents.
func (t *TUI) SetReplaySuppressed(s bool) { t.replaySuppressed.Store(s) }

// ReplaySuppressed reports whether frame rendering is paused for a replay.
func (t *TUI) ReplaySuppressed() bool { return t.replaySuppressed.Load() }

// ReplaySnapshot captures the CURRENTLY-mounted view's full canvas and window
// geometry for a scrollback replay. Must be called on the command loop
// (inside Apply): it composes the component tree's scene in full (cullFloor
// 0). Returns the canvas, the canvas row the visible window starts at
// (naturalVt — replay emits [savedWatermark, naturalVt)), the content end
// (canvas rows minus the chrome band), and the compose geometry.
func (t *TUI) ReplaySnapshot() (canvas []string, naturalVt, contentEnd, width, height int) {
	scene := t.buildSnapshot()
	canvas, _ = scene.compose(0)
	w, h := scene.TerminalW, scene.TerminalH
	contentEnd = len(canvas) - scene.ChromeHeight
	if contentEnd < 0 {
		contentEnd = 0
	}
	naturalVt = 0
	if contentEnd > h-scene.ChromeHeight {
		naturalVt = contentEnd - (h - scene.ChromeHeight)
	}
	return canvas, naturalVt, contentEnd, w, h
}

// listenResize reacts to terminal size changes by requesting a re-render.
// The platform-specific signal source lives in resize_unix.go / resize_windows.go
// (SIGWINCH is unavailable on Windows, where size changes are polled instead).
func (t *TUI) listenResize() {
	for {
		select {
		case <-resizeEvents(t.done):
			t.RequestRender()
		case <-t.done:
			return
		}
	}
}

// Stop restores terminal and stops goroutines.
// Does NOT clear screen, preserving scrollback.
// Stop restores the terminal and signals goroutines to exit.
//
// The ENTIRE restore (TUI reset sequences + Terminal.Stop, which drains input
// and re-enables cooked mode / auto-wrap / soft-reset) runs synchronously and
// completes BEFORE the done channel is closed. This is critical: Stop is often
// invoked from the control-event-reader goroutine (via /quit), and the main
// goroutine blocks on Stopped()/done in App.Run. If done were closed before
// Terminal.Stop finished, main would return and the process would exit while
// the terminal was still in raw/protocol mode — leaving the parent shell
// corrupted (missing DECAWM/soft-reset). See tui/terminal.go Stop() for the
// sequence ordering within the terminal itself.
//
// Stop may be called from multiple goroutines (Ctrl+C handler, /quit, App.Run);
// stopOnce guarantees the restore runs exactly once.
func (t *TUI) Stop() {
	t.stopOnce.Do(func() {
		t.stopped.Store(true)
		// Restore runs on the commandLoop. The renderLoop cannot interleave a
		// frame: it is blocked on snapReq, which only the commandLoop reads, and
		// the commandLoop is busy here until `done` is closed. Compositor.mu
		// serializes the terminal-output sequences with any in-flight Render.

		// The Compositor owns terminal protocol; it emits the shutdown
		// sequences (end synchronized output, reset SGR, cursor below content)
		// and shows the cursor. CSI 2026 must be turned off first; otherwise
		// the terminal stays locked and subsequent shell output is buffered.
		t.compositor.Restore()

		// Fully restore terminal state (cooked mode, auto-wrap, soft reset).
		// Must complete before we signal done so the process cannot exit
		// mid-restore.
		t.terminal.Stop()

		if t.keyLog != nil {
			// Best-effort flush/close of the optional keystroke trace log.
			_ = t.keyLog.close()
			t.keyLog = nil
		}

		t.started.Store(false)
		// Signal exit LAST, only after the terminal is fully restored.
		t.closeDone.Do(func() { close(t.done) })
	})
}

// HandleKeys returns false when the TUI has been stopped (Ctrl+C).
// Use Stopped() instead of polling this — it returns a channel you can block on.
func (t *TUI) HandleKeys() bool { return !t.stopped.Load() }

// Stopped returns a channel that is closed when the TUI engine stops
// (via Stop). Goroutines should block on this instead of polling HandleKeys().
func (t *TUI) Stopped() <-chan struct{} { return t.done }

// ── Key handling ──
// The TUI engine routes ALL input to the focused component. There are no global TUI-level scroll handlers for raw input events.
// key interceptors (handleScrollKey) or mouse event handlers. Scrolling is
// done via the terminal's native scrollbar.

// SetKeyLog enables asynchronous keystroke tracing to the given file path.
// The file is created with 0600 permissions and writes are buffered through a
// dedicated goroutine so the input hot path never blocks on disk I/O.
