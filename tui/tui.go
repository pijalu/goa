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

	focus        *FocusStack
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
	t.overlayStack = append(t.overlayStack, entry)
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
	for i, e := range t.overlayStack {
		if e == entry {
			t.overlayStack = append(t.overlayStack[:i], t.overlayStack[i+1:]...)
			break
		}
	}
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
func (t *TUI) RunLoops() {
	if t.loopsRunning.Swap(true) {
		return // already running
	}
	t.cmds = make(chan func(), 256)
	t.snapReq = make(chan chan<- *Scene)
	t.dirtyChan = make(chan struct{}, 1)
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
func (t *TUI) renderLoop() {
	for {
		select {
		case <-t.dirtyChan:
			if t.stopped.Load() {
				return
			}
			reply := make(chan *Scene, 1)
			t.snapReq <- reply
			scene := <-reply
			func() {
				defer recoverToLog("render")
				t.compositor.Render(scene)
			}()
			// Throttle to a maximum of ~60fps.
			select {
			case <-time.After(16 * time.Millisecond):
			case <-t.done:
				return
			}
		case <-t.done:
			return
		}
	}
}

// buildSnapshot builds a Scene from the current component state. Runs on the
// commandLoop (sole state owner), so it takes no lock — every mutation and
// every read of component state is serialized by the loop.
func (t *TUI) buildSnapshot() *Scene {
	w, h := t.terminal.Size()
	scene := t.buildScene(w, h)
	t.publishSize(scene.TerminalW, scene.TerminalH)
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
func (t *TUI) SetKeyLog(path string) error {
	kl, err := newKeyLogger(path)
	if err != nil {
		return err
	}
	t.keyLog = kl
	return nil
}

// logKey enqueues a formatted trace line when keystroke tracing is enabled.
func (t *TUI) logKey(format string, args ...any) {
	if t.keyLog == nil {
		return
	}
	t.keyLog.logf(format, args...)
}

func (t *TUI) handleKey(data string) {
	key := decodeKeyForRouting(data)
	focused := t.Focused()

	t.logKey("raw=%q key=%q focused=%T\n", data, key, focused)

	if t.handleTrappedInput(key, focused) {
		t.logKey("  → trapped\n")
		return
	}
	// Key release events (Kitty protocol) must be dropped before any routing.
	if t.ignoreKeyRelease(data, focused) {
		t.logKey("  → keyRelease\n")
		return
	}
	if t.routeToCapturingOverlay(data, key) {
		t.logKey("  → overlay\n")
		return
	}
	if t.handleDeleteLastKeys(key, focused) {
		t.logKey("  → deleteLastKeys\n")
		return
	}
	if t.handleCtrlC(key, focused) {
		t.logKey("  → ctrlc\n")
		return
	}
	if t.handleAppShortcuts(key) {
		t.logKey("  → appShortcut\n")
		return
	}

	if focused != nil {
		t.logKey("  → %T.HandleInput\n", focused)
		focused.HandleInput(key)
		t.RequestRender()
	}
}

// handleTrappedInput gives the focused component a chance to consume global
// keys such as Ctrl+C or Escape before any other routing.
func (t *TUI) handleTrappedInput(key string, focused Component) bool {
	if trap, ok := focused.(InputTrap); ok && trap.TrapInput(key) {
		t.RequestRender()
		return true
	}
	return false
}

// ignoreKeyRelease filters Kitty key-release events unless the focused
// component explicitly asks for them.
func (t *TUI) ignoreKeyRelease(data string, focused Component) bool {
	if !isKeyRelease(data) {
		return false
	}
	if f, ok := focused.(KeyReleaseAware); ok && f.WantsKeyRelease() {
		return false
	}
	return true
}

// handleDeleteLastKeys routes Ctrl+Backspace / Ctrl+Shift+Backspace to either
// the focused editor or the application-level "delete last message" callback.
func (t *TUI) handleDeleteLastKeys(key string, focused Component) bool {
	if matchesKey(key, "ctrl+shift+backspace") || matchesKey(key, "\x1b[3;6~") {
		if t.OnDeleteLast != nil {
			t.OnDeleteLast()
			t.RequestRender()
		}
		return true
	}
	if matchesKey(key, "ctrl+backspace") || matchesKey(key, "\x1b[3;5~") {
		if ed, ok := focused.(*Editor); ok && ed.Text() != "" {
			ed.HandleInput(key)
			t.RequestRender()
			return true
		}
		if t.OnDeleteLast != nil {
			t.OnDeleteLast()
			t.RequestRender()
			return true
		}
	}
	return false
}

// handleCtrlC clears the focused input when it has content; otherwise it stops
// the TUI.
func (t *TUI) handleCtrlC(key string, focused Component) bool {
	if !matchesKey(key, KeyCtrlC) {
		return false
	}
	if ed, ok := focused.(*Editor); ok && ed.Text() != "" {
		ed.Clear()
		t.RequestRender()
		return true
	}
	if ed, ok := focused.(*Input); ok && ed.Text() != "" {
		ed.Clear()
		t.RequestRender()
		return true
	}
	// Editor is empty: give the host a chance to cancel a pending main-input
	// request (e.g. /goal prompt) instead of quitting the application.
	if t.OnCancelInputRequest != nil && t.OnCancelInputRequest() {
		t.RequestRender()
		return true
	}
	t.Stop()
	return true
}

// handleAppShortcuts handles Ctrl+O expand/collapse, Ctrl+G goal-bubble toggle,
// Alt+M mode change, Shift+Tab thinking-level cycle, Ctrl+L model selector,
// Ctrl+T thinking-blocks toggle.
func (t *TUI) handleAppShortcuts(key string) bool {
	if t.handleToggleExpand(key) {
		return true
	}
	fn, ok := t.resolveAppShortcut(key)
	if !ok {
		return false
	}
	t.invokeCallback(fn)
	return true
}

// resolveAppShortcut maps a decoded key to its application-level callback.
// It accounts for terminals that emit an alt+printable character instead of
// the ESC+<base> sequence for Option-key combinations on macOS. A flat table
// keeps the dispatch cyclomatic-complexity low (one loop, no big switch).
func (t *TUI) resolveAppShortcut(key string) (func(), bool) {
	altKey := altKeyName(key)
	for _, sc := range appShortcuts {
		if sc.matches(key, altKey) {
			return sc.callback(t), true
		}
	}
	return nil, false
}

// appShortcut is one application-level keybinding: a set of accepted key names
// (plus the macOS Option-alias form) and the callback it resolves to.
type appShortcut struct {
	keys     []string // exact key names (and alt+uppercase variants)
	altAlias string   // optional macOS Option-key alias (e.g. "alt+m")
	callback func(t *TUI) func()
}

func (s appShortcut) matches(key, altKey string) bool {
	for _, k := range s.keys {
		if matchesKey(key, k) {
			return true
		}
	}
	return s.altAlias != "" && matchesKey(altKey, s.altAlias)
}

// appShortcuts is the ordered table consumed by resolveAppShortcut.
var appShortcuts = []appShortcut{
	{keys: []string{"ctrl+g"}, callback: func(t *TUI) func() { return t.OnToggleGoalBubble }},
	{keys: []string{"alt+e", "alt+E"}, altAlias: "alt+e", callback: func(t *TUI) func() { return t.OnEditSteering }},
	{keys: []string{"alt+m", "alt+M"}, altAlias: "alt+m", callback: func(t *TUI) func() { return t.OnChangeMode }},
	{keys: []string{"alt+o", "alt+O"}, altAlias: "alt+o", callback: func(t *TUI) func() { return t.OnOpenModeSelector }},
	{keys: []string{"ctrl+shift+m"}, callback: func(t *TUI) func() { return t.OnCycleAutonomy }},
	{keys: []string{KeyShiftTab}, callback: func(t *TUI) func() { return t.OnCycleThinkingLevel }},
	{keys: []string{KeyCtrlL}, callback: func(t *TUI) func() { return t.OnChangeModel }},
	{keys: []string{KeyCtrlT}, callback: func(t *TUI) func() { return t.OnToggleThinkingBlocks }},
	{keys: []string{"ctrl+x"}, callback: func(t *TUI) func() { return t.OnOpenAgentTabs }},
}

func (t *TUI) invokeCallback(fn func()) {
	if fn != nil {
		fn()
		t.RequestRender()
	}
}

// decodeKeyForRouting converts raw terminal bytes into a key name for
// matching, but preserves raw text/paste events so multi-character input is
// not split into individual key presses.
func decodeKeyForRouting(data string) string {
	// Multi-character data that does not begin with an escape sequence is raw
	// text from a bracketed paste (or similar bulk input). Pass it through
	// unchanged so components can detect and handle pastes.
	if len(data) > 1 && data[0] != '\x1b' {
		return data
	}
	decoded := decodeKeys([]byte(data))
	if len(decoded) > 0 && decoded[0] != "" {
		return decoded[0]
	}
	return data
}

// routeToCapturingOverlay sends input to the topmost capturing overlay, if
// any. Returns true when the input was consumed by the overlay.
func (t *TUI) routeToCapturingOverlay(data, key string) bool {
	if len(t.overlayStack) == 0 {
		return false
	}
	top := t.overlayStack[len(t.overlayStack)-1]
	if !top.opts.CaptureInput {
		return false
	}
	// Overlays receive the decoded key name for control keys, but raw data for
	// pasted text so their own paste handling can run.
	if len(data) > 1 && data[0] != '\x1b' {
		top.comp.HandleInput(data)
	} else {
		top.comp.HandleInput(key)
	}
	t.RequestRender()
	return true
}

// ShowSelector displays a channel-based interactive selector as an overlay.
// The caller blocks on the returned channel until the user selects or cancels.
// title is shown at the top; currentValue is marked with a ✓ indicator.
func (t *TUI) ShowSelector(title string, items []SelectorItem, currentValue string) <-chan string {
	result := make(chan string, 1)
	sel := NewSelector(title, items, currentValue, result)
	sel.SetTUI(t)
	_, termH := t.terminal.Size()
	if termH < 4 {
		// Terminal too small for overlay — render inline instead
		result := make(chan string, 1)
		go func() {
			result <- ""
		}()
		return result
	}
	h := len(items) + 4
	if h > termH {
		h = termH
	}
	opts := OverlayOptions{
		CaptureInput: true,
		Height:       h,
	}
	handle := t.ShowOverlay(sel, opts)
	sel.SetDone(func() {
		handle.Hide()
	})
	return result
}

// ShowInput displays a single-line input prompt as an overlay.
// The caller blocks on the returned channel until the user submits or cancels.
//
// Deprecated: this spawns a throwaway overlay Input that bypasses the main
// input zone. New code must capture text via the main input line
// (App.requestMainInput / core.Context.RequestMainInput) per the "Input
// discipline" guideline in docs/TUI.md. Retained for tests and any external
// callers; production code no longer invokes it.
func (t *TUI) ShowInput(prompt, current string) <-chan string {
	result := make(chan string, 1)
	in := NewInput()
	in.SetText(current)
	comp := &inputOverlay{prompt: prompt, input: in, result: result}
	in.SetOnSubmit(func(text string) {
		select {
		case result <- text:
		default:
		}
		if comp.done != nil {
			comp.done()
		}
	})
	opts := OverlayOptions{
		CaptureInput: true,
		Height:       3,
	}
	handle := t.ShowOverlay(comp, opts)
	comp.SetDone(func() {
		handle.Hide()
	})
	return result
}

// inputOverlay wraps an Input with a prompt label for use as an overlay.
type inputOverlay struct {
	prompt string
	input  *Input
	result chan string
	done   func()
}

func (o *inputOverlay) SetDone(fn func()) { o.done = fn }
func (o *inputOverlay) Render(width int) []string {
	var lines []string
	lines = append(lines, padToWidth(o.prompt, width))
	lines = append(lines, o.input.Render(width)...)
	return lines
}
func (o *inputOverlay) HandleInput(data string) {
	if matchesKey(data, KeyEscape) || matchesKey(data, KeyCtrlC) {
		if o.done != nil {
			o.done()
		}
		select {
		case o.result <- "":
		default:
		}
		return
	}
	o.input.HandleInput(data)
}
func (o *inputOverlay) SetFocused(f bool) { o.input.SetFocused(f) }
func (o *inputOverlay) Focused() bool     { return o.input.Focused() }
func (o *inputOverlay) Invalidate()       {}

// handleToggleExpand handles Ctrl+O to toggle ALL tool components between
// Summary (collapsed, N-line preview) and Full (expanded) view for the
// running session. Previously it toggled only the last widget; the global
// toggle matches the spec (one key flips every tool block) and is honored by
// every widget via the ChatViewport's ToolViewPolicy.
func (t *TUI) handleToggleExpand(data string) bool {
	if !matchesKey(data, "ctrl+o") {
		return false
	}
	cv := t.findChatViewport()
	if cv == nil {
		return false
	}
	cv.ToggleAllToolsView()
	t.RequestRender()
	return true
}

// findChatViewport finds the ChatViewport child, if any.
func (t *TUI) findChatViewport() *ChatViewport {
	for _, child := range t.children {
		if cv, ok := child.(*ChatViewport); ok {
			return cv
		}
	}
	return nil
}

// RenderNow synchronously renders one frame and returns the rendered lines.
// Intended for tests; production renders go through the throttled renderLoop.
// RenderNow synchronously renders one frame and returns the composed canvas.
// Intended for tests; production renders go through the throttled renderLoop.
func (t *TUI) RenderNow() []string { return t.renderNow() }

// SendKey injects a decoded key into the TUI, routes it to the focused
// component, and synchronously renders one frame. This is the primary API for
// agentic tests that drive the UI without a real terminal.
func (t *TUI) SendKey(key string) {
	t.handleKey(key)
	t.RenderNow()
}

// renderNow assembles a protocol-free Scene (layers + cursor + DOM nodes) from the
// component tree and overlays, then hands it to the Compositor which owns all
// terminal-protocol output. The TUI never
// emits escape sequences or touches the diff baseline.
func (t *TUI) renderNow() []string {
	if !t.started.Load() || t.stopped.Load() {
		return nil
	}

	w, h := t.terminal.Size()
	scene := t.buildScene(w, h)
	t.compositor.Render(scene)
	t.publishSize(scene.TerminalW, scene.TerminalH)
	return t.compositor.Buffer()
}

// buildScene renders every child component into a stacked base Layer and every
// overlay into a positioned overlay Layer, producing the protocol-free Scene
// consumed by both the Compositor and the AgentView. Layer Rect.Y accumulates
// for base layers; overlays are positioned relative to the visible viewport.
// Components that expose a viewport height or total height are culled so the
// compositor only sees the visible tail, while the absolute Y accounting
// preserves the full virtual buffer height for correct scrolling.
// The focused editor's CURSOR_MARKER is extracted into Scene.Cursor (explicit,
// grapheme-aware) and stripped from layer content.
func (t *TUI) buildScene(w, h int) *Scene {
	scene := &Scene{TerminalW: w, TerminalH: h}
	rendered, _ := t.renderChildren(w, h)
	scene.Layers, scene.Nodes = t.buildBaseLayers(rendered, w, h)
	scene.ChromeHeight = t.bottomChromeHeight(rendered)
	scene.OverlayCapturesInput = t.buildOverlayLayers(scene, w, h)
	extractCursorMarker(scene)
	return scene
}

// bottomChromeHeight returns the total rendered height of the fixed chrome
// stacked BELOW the scrollable transcript (the HeightAllocated child): status
// bar, goal/steering bubbles, bg panel, input editor, footer. These rows must
// never enter terminal scrollback when the transcript scrolls. Children above
// the transcript (the header) scroll with it and are not counted.
func (t *TUI) bottomChromeHeight(rendered [][]string) int {
	transcriptIdx := t.transcriptChildIndex()
	if transcriptIdx < 0 {
		return 0
	}
	chrome := 0
	for i := transcriptIdx + 1; i < len(t.children); i++ {
		chrome += len(rendered[i])
	}
	return chrome
}

// transcriptChildIndex returns the index of the scrollable fill child (the
// conversation viewport), or -1 when none is present. It is the single
// HeightAllocated component; everything after it is pinned bottom chrome.
func (t *TUI) transcriptChildIndex() int {
	for i, child := range t.children {
		if _, ok := child.(HeightAllocated); ok {
			return i
		}
	}
	return -1
}

// renderChildren renders all base children, setting viewport/allocated heights
// first and returning the per-child rendered lines plus the total chrome height.
func (t *TUI) renderChildren(w, h int) ([][]string, int) {
	rendered := make([][]string, len(t.children))
	chromeHeight := 0
	var fills []int

	for i, child := range t.children {
		if _, ok := child.(HeightAllocated); ok {
			fills = append(fills, i)
			continue
		}
		t.setViewportHeight(child, h)
		rendered[i] = child.Render(w)
		chromeHeight += len(rendered[i])
	}
	if len(fills) > 0 {
		budget := (h - chromeHeight) / len(fills)
		if budget < 0 {
			budget = 0
		}
		for _, idx := range fills {
			t.children[idx].(HeightAllocated).SetAllocatedHeight(budget)
			t.setViewportHeight(t.children[idx], h)
			rendered[idx] = t.children[idx].Render(w)
			chromeHeight += len(rendered[idx])
		}
	}
	return rendered, chromeHeight
}

func (t *TUI) setViewportHeight(c Component, h int) {
	if vh, ok := c.(interface{ SetViewportHeight(int) }); ok {
		vh.SetViewportHeight(h)
	}
}

func (t *TUI) totalHeight(c Component, renderedLen int) int {
	if hr, ok := c.(interface{ TotalHeight() int }); ok {
		if th := hr.TotalHeight(); th > renderedLen {
			return th
		}
	}
	return renderedLen
}

// buildBaseLayers converts rendered children into base layers and agent nodes.
// It also collects each child's transient popup (PopupRenderer) and emits those
// as LayerOverlay layers so the base canvas height stays constant (see
// PopupRenderer).
func (t *TUI) buildBaseLayers(rendered [][]string, w, h int) ([]Layer, []AgentNode) {
	var layers []Layer
	var nodes []AgentNode
	var seeds []popupSeed
	y := 0
	for i, child := range t.children {
		lines := rendered[i]
		totalH := t.totalHeight(child, len(lines))
		if len(lines) == 0 {
			y += totalH
			continue
		}
		rectY := y
		if totalH > len(lines) {
			rectY = y + totalH - len(lines)
		}
		rect := Rect{X: 0, Y: rectY, W: w, H: len(lines)}
		layers = append(layers, Layer{
			Name:    componentLayerName(child),
			Kind:    LayerBase,
			Rect:    rect,
			Content: lines,
		})
		nodes = append(nodes, agentNodeFor(child, rect, lines))
		if pr, ok := child.(PopupRenderer); ok {
			if pl := pr.PopupLines(w); len(pl) > 0 {
				seeds = append(seeds, popupSeed{lines: pl, rect: rect})
			}
		}
		y += totalH
	}
	popLayers, popNodes := buildPopupOverlays(seeds, w, h, y)
	layers = append(layers, popLayers...)
	nodes = append(nodes, popNodes...)
	return layers, nodes
}

// popupSeed pairs a PopupRenderer's transient lines with the canvas rect of the
// base component that owns them, so buildPopupOverlays can position the popup
// relative to its owner after the stacked base height is known.
type popupSeed struct {
	lines []string
	rect  Rect
}

// buildPopupOverlays turns popup seeds into LayerOverlay layers. Each popup
// floats ABOVE its owning component as a viewport-relative overlay, so the
// base canvas height never changes and opening/closing a popup can never push
// base content into terminal scrollback.
//
// Placement: prefer directly above the owner (the conventional autocomplete
// position, and overflow-safe when the owner is bottom-anchored like the
// editor). If there is not enough room above, fall back to below the owner.
// The result is clamped to the visible viewport so the overlay never extends
// the canvas beyond the terminal height (which would itself trigger a scroll).
func buildPopupOverlays(seeds []popupSeed, w, h, baseHeight int) ([]Layer, []AgentNode) {
	if len(seeds) == 0 {
		return nil, nil
	}
	viewportStart := baseHeight - h
	if viewportStart < 0 {
		viewportStart = 0
	}
	var layers []Layer
	var nodes []AgentNode
	for _, s := range seeds {
		popupH := len(s.lines)
		if popupH <= 0 {
			continue
		}
		lines := s.lines
		if popupH > h {
			lines = append([]string(nil), lines[:h]...)
			popupH = h
		}
		screenTop := s.rect.Y - viewportStart
		if screenTop < 0 {
			screenTop = 0
		}
		// Prefer above the owner; fall back to below if it does not fit.
		y := screenTop - popupH
		if y < 0 {
			y = screenTop + s.rect.H
		}
		// Clamp into the visible viewport so the overlay never grows the canvas
		// past the terminal height.
		if y+popupH > h {
			y = h - popupH
		}
		if y < 0 {
			y = 0
		}
		rect := Rect{X: 0, Y: y, W: w, H: popupH}
		content := append([]string(nil), lines...)
		layers = append(layers, Layer{
			Name:    "popup",
			Kind:    LayerOverlay,
			Z:       1,
			Rect:    rect,
			Content: content,
		})
		nodes = append(nodes, AgentNode{Name: "popup", Type: "*tui.Popup", Rect: rect})
	}
	return layers, nodes
}

// buildOverlayLayers appends overlay layers to the scene and reports whether any
// overlay captures input.
func (t *TUI) buildOverlayLayers(scene *Scene, w, h int) bool {
	captures := false
	for _, ov := range t.overlayStack {
		olines := ov.comp.Render(w)
		if len(olines) == 0 {
			continue
		}
		if ov.opts.CaptureInput {
			captures = true
		}
		oh := clampOverlayHeight(len(olines), h)
		startRow := overlayStartRow(ov.opts, oh, h)
		rect := Rect{X: 0, Y: startRow, W: w, H: oh}
		scene.Layers = append(scene.Layers, Layer{
			Name:    componentLayerName(ov.comp),
			Kind:    LayerOverlay,
			Z:       1 + len(scene.Layers),
			Rect:    rect,
			Content: append([]string(nil), olines[:oh]...),
		})
		scene.Nodes = append(scene.Nodes, agentNodeFor(ov.comp, rect, olines[:oh]))
	}
	return captures
}

// agentNodeFor builds a lightweight AgentNode (Name, Type, Rect, Focused)
// from a component and its rendered layer. It intentionally does NOT compute
// the node's Text (an O(n) ansi.Strip+Join over the layer's lines): that text
// is only consumed by AI tooling via AgentFrame, never by the live render
// path, so it is filled lazily in Scene.AgentFrame to avoid an O(history)
// string allocation every streaming frame for the chat layer.
func agentNodeFor(c Component, rect Rect, lines []string) AgentNode {
	node := AgentNode{
		Name: componentLayerName(c),
		Type: fmt.Sprintf("%T", c),
		Rect: rect,
	}
	if f, ok := c.(Focusable); ok {
		node.Focused = f.Focused()
	}
	return node
}

// extractCursorMarker scans layers (topmost overlay first, then base layers)
// for the CURSOR_MARKER emitted by the focused input, sets Scene.Cursor to
// its absolute (row, col) position, and strips the marker. col is
// grapheme-aware (matches the terminal).
//
// When a capturing overlay is open, the base editor's cursor must NOT be used:
// the overlay owns input and a non-cursor overlay (like the tab picker) should
// leave the hardware cursor hidden.
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
