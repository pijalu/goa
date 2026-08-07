// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/term"
)

// Terminal defines the interface for terminal I/O.
type Terminal interface {
	Start(onInput func(string), onResize func())
	Stop()
	Write(p []byte) (n int, err error)
	WriteString(s string)
	Size() (width, height int)
	SetRaw() (restore func(), err error)
	HideCursor()
	ShowCursor()
	ClearScreen()
	SetTitle(title string)
	io.Writer
}

// screenOwnerCount tracks live ProcessTerminal sessions holding raw mode —
// i.e. a full-screen TUI currently owns the display. While it is non-zero,
// stray process-level writes to stdout/stderr (macOS libmalloc warnings such
// as "MallocStackLogging: can't turn off ...", C libraries, child processes
// inheriting fd 2) must NOT reach the TTY: they bypass Go entirely and
// corrupt the differential frame. The internal/app crash-log tee consults
// OwnsScreen to decide whether to forward captured stderr to the terminal
// or only to the log file.
var screenOwnerCount atomic.Int64

// OwnsScreen reports whether a full-screen terminal session currently owns
// the display (raw mode acquired by a ProcessTerminal).
func OwnsScreen() bool { return screenOwnerCount.Load() > 0 }

// claimScreen/releaseScreen bracket raw-mode ownership. Counter-based so
// overlapping sessions (e.g. a restarted TUI before the old one fully
// stops) resolve correctly.
func claimScreen()   { screenOwnerCount.Add(1) }
func releaseScreen() { screenOwnerCount.Add(-1) }

// ProcessTerminal implements Terminal with raw mode and Kitty keyboard protocol.
type ProcessTerminal struct {
	fd       int
	onInput  func(string)
	onResize func()
	restore  func()
	running  bool
	done     chan struct{}

	// reader is the terminal's input source. Production uses os.Stdin;
	// tests substitute an os.Pipe to drive input deterministically.
	reader io.Reader

	// readLoopDone is closed when the readLoop goroutine exits. Stop() waits
	// on it (after interrupting the blocking read) so a successor engine — the
	// setup wizard, or the relaunched app after the wizard — always starts
	// with exactly one reader on stdin. Without this, the stale readLoop kept
	// reading os.Stdin and stole keystrokes from the new engine, making the
	// wizard GUI appear frozen/unresponsive.
	readLoopDone chan struct{}

	// screenClaimed records that Start acquired raw mode and claimed screen
	// ownership, so Stop releases it exactly once (even on partial paths).
	screenClaimed bool

	// lastGoodSize caches the most recent plausible terminal size. The
	// TIOCGWINSZ ioctl can transiently fail (or return a degenerate size) on
	// a real terminal — e.g. during a SIGWINCH race or an emulator resize
	// burst — and Size() would otherwise fall back to 80x24 for a single
	// frame. That one-frame blip makes the compositor take its
	// resize/full-repaint path and repaint the header (the bugs.md "Mascot
	// redraw" regression: the logo flashing mid-session during a tool call).
	// A transient misread is indistinguishable from a real resize at the
	// compositor, so Size() filters it at the source.
	lastGoodW int
	lastGoodH int
	sizeMu    sync.Mutex

	// Persistent input buffer — accumulates partial sequences across reads
	stdinBuffer *StdinBuffer

	// Kitty keyboard protocol
	mu           sync.Mutex
	kittyActive  bool
	protoPending atomic.Bool
	protoBuf     string // buffer for split protocol responses
	protoTimer   *time.Timer

	// escapeDebounce handles the classic Escape-vs-CSI-start ambiguity.
	// When a single 0x1b byte arrives, we wait briefly for more bytes
	// before treating it as an Escape key press. If completing bytes
	// arrive in time, the sequence is merged and forwarded as one.
	escapePending atomic.Bool
	escapeTimer   *time.Timer
	escapeMu      sync.Mutex
}

// NewProcessTerminal creates a ProcessTerminal.
func NewProcessTerminal() *ProcessTerminal {
	return &ProcessTerminal{
		fd:          int(os.Stdin.Fd()),
		reader:      os.Stdin,
		stdinBuffer: NewStdinBuffer(),
	}
}

// Start enters raw mode, enables bracketed paste, queries Kitty protocol.
// If raw mode setup fails (e.g., not a terminal), the read loop still starts
// so the application can process keyboard input from pipes or other sources.
func (t *ProcessTerminal) Start(onInput func(string), onResize func()) {
	t.onInput = onInput
	t.onResize = onResize
	t.done = make(chan struct{})

	restore, err := t.SetRaw()
	if err == nil {
		t.restore = restore
		t.running = true
		// Raw mode acquired: a full-screen session owns the display from
		// here until Stop. Stray fd-level writes must be kept off the TTY
		// (see OwnsScreen).
		t.screenClaimed = true
		claimScreen()
		t.stdinBuffer = NewStdinBuffer()

		// Enable Windows VT input (no-op on other platforms)
		enableWindowsVTInput()

		// Disable auto-wrap (DECAWM) for the session. The compositor positions
		// every row by absolute CUP and truncates content to the terminal width,
		// so auto-wrap provides nothing — and it is actively harmful: a row
		// exactly `width` columns fills the last column and leaves the terminal
		// in a pending-wrap state, so the next line-feed or write wraps onto an
		// extra row and every subsequent compositor row index is off by one
		// (the scrollback line-duplication in bugs.md). Stop() re-enables it
		// (\x1b[?7h) so the parent shell wraps normally.
		os.Stdout.WriteString("\x1b[?7l")

		// Enable bracketed paste mode
		os.Stdout.WriteString("\x1b[?2004h")

		// Query Kitty keyboard protocol
		t.queryKitty()
	}

	// Always start the read loop — even without raw mode, this allows
	// processing commands from pipes and non-terminal stdin.
	t.readLoopDone = make(chan struct{})
	go t.readLoop()
}

// queryKitty sends the Kitty keyboard protocol query and starts negotiation.
// Query: ESC [ > flags u  ESC [ ? u  ESC [ c
// flags = 1 (disambiguate) — keeps Ctrl+Enter (\x1b[13;5u) distinct
// queryKitty sends the Kitty keyboard protocol query and starts negotiation.
// Query: ESC [ > flags u  ESC [ ? u  ESC [ c
// flags = 7 (1|2|4 = disambiguate + event types + alternate keys).
func (t *ProcessTerminal) queryKitty() {
	t.mu.Lock()
	t.protoPending.Store(true)
	t.protoBuf = ""
	t.mu.Unlock()
	os.Stdout.WriteString("\x1b[>7u\x1b[?u\x1b[c")

	// Fallback timer: if no response within 150ms, assume no Kitty support.
	t.protoTimer = time.AfterFunc(150*time.Millisecond, func() {
		if !t.protoPending.Load() {
			return
		}
		t.protoPending.Store(false)
		t.enableModifyOtherKeys()
	})
}

func (t *ProcessTerminal) enableModifyOtherKeys() {
	if !t.running || t.kittyActive {
		return
	}
	os.Stdout.WriteString("\x1b[>4;2m") // Enable modifyOtherKeys mode 2
}

const escapeDebounceTimeout = 20 * time.Millisecond

// readLoop reads from stdin and dispatches input events.
// It handles the Escape-vs-CSI-start ambiguity: a bare 0x1b byte
// is debounced for escapeDebounceTimeout before emitting as Escape.
func (t *ProcessTerminal) readLoop() {
	defer func() {
		// Signal Stop() that the loop exited so it can stop waiting and a
		// successor engine can safely start its own reader on stdin.
		if t.readLoopDone != nil {
			close(t.readLoopDone)
		}
	}()
	buf := make([]byte, 256)
	for {
		select {
		case <-t.done:
			return
		default:
		}

		// If an escape debounce is pending, check for more bytes with a timeout.
		if t.escapePending.Load() {
			t.pollEscapeDebounce()
			continue
		}

		n, err := t.reader.Read(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
		}

		// Discard input that arrives after Stop(): the readLoop can be blocked
		// inside Read when done is closed (a console read cannot be preempted
		// on every platform), so it may wake with data after shutdown. Never
		// dispatch it — the next engine owns stdin now.
		select {
		case <-t.done:
			return
		default:
		}

		data := string(buf[:n])

		// Bare 0x1b byte: could be Escape key or start of a CSI/SS3 sequence
		// that arrived in a separate TCP segment. Debounce before dispatching.
		if n == 1 && buf[0] == 0x1b && !t.protoPending.Load() {
			t.startEscapeDebounce()
			continue
		}

		if t.protoPending.Load() {
			t.handleProtocolBytes(data)
		} else {
			t.forwardToInput(data)
		}
	}
}

// pollEscapeDebounce waits for more bytes after a bare 0x1b with a
// brief timeout. If bytes arrive, they are merged with the pending
// escape and forwarded as a complete sequence. If the timeout fires,
// the escape is forwarded as a standalone Escape key press.
func (t *ProcessTerminal) pollEscapeDebounce() {
	t.escapeMu.Lock()
	if !t.escapePending.Load() {
		t.escapeMu.Unlock()
		return
	}
	t.escapeMu.Unlock()

	// Set a brief read deadline so we don't block forever.
	_ = setReadDeadline(t.reader, time.Now().Add(escapeDebounceTimeout))

	buf := make([]byte, 256)
	n, err := t.reader.Read(buf)

	// The fallback timer may have already emitted the bare ESC while this
	// read was blocked (Windows consoles ignore read deadlines, so the read
	// waits for the user's next keystroke). What we just read is then the
	// NEXT keypress and must be forwarded on its own — re-prefixing it with
	// the stale ESC silently eats it (the wizard's back/cancel navigation
	// would drop the key that follows every Escape).
	alreadyEmitted := !t.escapePending.Load()

	// Cancel the pending debounce regardless of outcome.
	t.escapeMu.Lock()
	t.escapePending.Store(false)
	if t.escapeTimer != nil {
		t.escapeTimer.Stop()
		t.escapeTimer = nil
	}
	t.escapeMu.Unlock()

	// Clear the read deadline so subsequent reads block normally.
	_ = setReadDeadline(t.reader, time.Time{})

	if alreadyEmitted {
		// The timer emitted the ESC; forward what we read as its own key.
		if err == nil && n > 0 {
			t.forwardToInput(string(buf[:n]))
		}
		return
	}

	if err != nil || n == 0 {
		// No more data arrived — this is a real Escape key press.
		t.forwardToInput("\x1b")
		return
	}

	// More data arrived: merge with the pending escape and forward.
	t.forwardToInput("\x1b" + string(buf[:n]))
}

// startEscapeDebounce starts (or resets) the escape debounce timer.
func (t *ProcessTerminal) startEscapeDebounce() {
	t.escapeMu.Lock()
	defer t.escapeMu.Unlock()

	t.escapePending.Store(true)
	if t.escapeTimer != nil {
		t.escapeTimer.Stop()
	}
	// Fallback timer: if pollEscapeDebounce doesn't run (e.g., readLoop is
	// stuck in a stalled read), this timer ensures we don't lose the Escape.
	t.escapeTimer = time.AfterFunc(escapeDebounceTimeout*2, func() {
		if !t.escapePending.Load() {
			return
		}
		t.escapeMu.Lock()
		t.escapePending.Store(false)
		t.escapeTimer = nil
		t.escapeMu.Unlock()
		t.forwardToInput("\x1b")
	})
}

// setReadDeadline sets a read deadline on r when it supports one (pollable
// files such as os.Stdin on Unix and os.Pipe on any platform). Windows
// console handles do not support deadlines; the platform-specific
// interruptStdinRead falls back to CancelIoEx for those.
func setReadDeadline(r io.Reader, t time.Time) error {
	if rd, ok := r.(interface{ SetReadDeadline(time.Time) error }); ok {
		return rd.SetReadDeadline(t)
	}
	return os.ErrNoDeadline
}

// handleProtocolBytes processes raw bytes during protocol negotiation.
// Accumulates data in protoBuf, scans for complete protocol responses,
// and forwards any remaining non-protocol data as input.
func (t *ProcessTerminal) handleProtocolBytes(data string) {
	t.protoBuf += data
	t.scanProtoBuf()
}

// scanProtoBuf scans protoBuf for complete protocol responses.
// All sequences before a protocol response are forwarded as input.
// After a protocol response is found, any remaining data is forwarded.
func (t *ProcessTerminal) scanProtoBuf() {
	buf := t.protoBuf
	if len(buf) == 0 {
		return
	}

	pos := 0
	foundResponse := false
	nonProtoEnd := 0

	for pos < len(buf) {
		if buf[pos] != '\x1b' {
			foundResponse = true
			t.finishProto()
			break
		}

		seq, n := extractEscapeSeq(buf[pos:])
		if n == 0 {
			break
		}

		if isKittyFlagsResponse(seq) || isDADeviceAttributes(seq) {
			t.handleProtoResponse(seq)
			foundResponse = true
			pos += n
			break
		}

		pos += n
		nonProtoEnd = pos
	}

	t.applyScanResult(buf, foundResponse, pos, nonProtoEnd)
}

func (t *ProcessTerminal) applyScanResult(buf string, foundResponse bool, pos, nonProtoEnd int) {
	if foundResponse {
		t.protoPending.Store(false)
		t.protoBuf = ""
		if nonProtoEnd > 0 {
			t.forwardToInput(buf[:nonProtoEnd])
		}
		if pos < len(buf) {
			t.forwardToInput(buf[pos:])
		}
		return
	}

	if nonProtoEnd > 0 {
		t.forwardToInput(buf[:nonProtoEnd])
		t.protoBuf = buf[nonProtoEnd:]
		return
	}

	if len(buf) > 128 {
		t.finishProto()
		t.forwardToInput(buf)
	}
}

// extractEscapeSeq extracts a complete escape sequence starting at buf[0].
// Delegates to the shared nextSequence parser in escape.go.
func extractEscapeSeq(buf string) (string, int) {
	return nextSequence([]byte(buf))
}

// isKittyFlagsResponse checks if a sequence is a Kitty flags response: ESC [ ? NNN u
func isKittyFlagsResponse(seq string) bool {
	return strings.HasPrefix(seq, "\x1b[?") && strings.HasSuffix(seq, "u")
}

// isDA checks if a sequence is a Device Attributes response: ESC [ ? NNN c
func isDADeviceAttributes(seq string) bool {
	return strings.HasPrefix(seq, "\x1b[?") && strings.HasSuffix(seq, "c")
}

// handleProtoResponse processes a detected protocol response.
// Sets kittyActive so the TUI knows Kitty keyboard protocol is available.
func (t *ProcessTerminal) handleProtoResponse(seq string) {
	if t.protoTimer != nil {
		t.protoTimer.Stop()
		t.protoTimer = nil
	}
	if !t.running {
		return
	}
	if isKittyFlagsResponse(seq) {
		t.kittyActive = true
	}
	t.protoPending.Store(false)
}

// finishProto aborts protocol negotiation (timeout or unexpected data).
func (t *ProcessTerminal) finishProto() {
	if t.protoTimer != nil {
		t.protoTimer.Stop()
		t.protoTimer = nil
	}
	t.protoPending.Store(false)
	t.protoBuf = ""
}

// forwardToInput forwards raw bytes through the persistent StdinBuffer
// and sends complete sequences directly to the input handler (no decodeKeys).
// The StdinBuffer emits complete sequences; TUI.handleKey
// routes them raw via matchesKey().
//
// Panic safety: onInput runs in the readLoop goroutine, which is the only
// stdin consumer. A panic there would permanently freeze input. Any panic is
// recovered, logged to stderr, and the loop continues so a single malformed
// event cannot kill all keyboard input.
func (t *ProcessTerminal) forwardToInput(data string) {
	events := t.stdinBuffer.Process([]byte(data))
	for _, ev := range events {
		select {
		case <-t.done:
			return
		default:
		}
		if t.onInput != nil {
			t.dispatchInput(ev)
		}
	}
}

// dispatchInput invokes the registered input handler with panic recovery.
func (t *ProcessTerminal) dispatchInput(ev string) {
	defer func() {
		if r := recover(); r != nil {
			fmt.Fprintf(os.Stderr, "goa: input handler panic recovered: %v\n", r)
		}
	}()
	t.onInput(ev)
}

// Stop restores terminal settings.
// Shutdown order: disable input-generating protocols before
// draining, then reset attributes, flush output, and finally restore cooked
// mode so the parent shell receives a clean terminal.
func (t *ProcessTerminal) Stop() {
	defer func() {
		if r := recover(); r != nil {
			// suppress double-close panics during shutdown
		}
	}()
	// Release screen ownership first (exactly once, on any path): from here
	// on, captured process stderr may be forwarded to the terminal again.
	if t.screenClaimed {
		t.screenClaimed = false
		releaseScreen()
	}
	if !t.running {
		// Already stopped or raw mode was never entered. Still attempt a
		// terminal restore in case Stop is called after a partial startup.
		if t.restore != nil {
			t.restore()
			t.restore = nil
		}
		// Start() always launches the readLoop (even when raw mode fails, so
		// pipe/passthrough input still works): signal it to exit and wait, so
		// no reader is left on stdin for a successor engine.
		if t.done != nil {
			close(t.done)
		}
		t.shutdownReadLoop()
		return
	}

	// Stop any pending protocol negotiation timer so a late fallback cannot
	// re-enable modifyOtherKeys/Kitty after shutdown.
	if t.protoTimer != nil {
		t.protoTimer.Stop()
		t.protoTimer = nil
	}
	t.protoPending.Store(false)
	t.protoBuf = ""

	// Cancel any pending escape debounce.
	t.escapeMu.Lock()
	t.escapePending.Store(false)
	if t.escapeTimer != nil {
		t.escapeTimer.Stop()
		t.escapeTimer = nil
	}
	t.escapeMu.Unlock()

	// Disable bracketed paste first; this stops the terminal from wrapping
	// pasted content in 200~...201~ sequences.
	os.Stdout.WriteString("\x1b[?2004l")

	// Disable Kitty keyboard protocol and modifyOtherKeys before draining so
	// no new escape sequences are generated while we read out the queue.
	os.Stdout.WriteString("\x1b[<u")    // Disable Kitty protocol
	os.Stdout.WriteString("\x1b[>4;0m") // Disable modifyOtherKeys
	t.kittyActive = false

	// Ensure the disable sequences have actually left the process before
	// restoring cooked mode; otherwise they can be buffered and leak into
	// the parent shell (observed in Ghostty).
	_ = os.Stdout.Sync()

	// Drain any queued input (key releases, late protocol responses).
	t.drainInput(1000, 50)

	// Final reset: clear SGR, show cursor, re-enable auto-wrap, stop cursor
	// blinking, and perform a soft terminal reset. This restores the terminal
	// emulator state (not just termios) so the parent shell renders correctly
	// after exit. The soft reset clears lingering modes/margins that can cause
	// wrapping corruption (observed in Ghostty).
	os.Stdout.WriteString("\x1b[0m")   // Reset SGR
	os.Stdout.WriteString("\x1b[?25h") // Show cursor
	os.Stdout.WriteString("\x1b[?7h")  // Enable auto-wrap (DECAWM)
	os.Stdout.WriteString("\x1b[?12l") // Stop blinking cursor
	os.Stdout.WriteString("\x1b[!p")   // Soft reset (DECSTR)
	os.Stdout.WriteString("\r\n")
	_ = os.Stdout.Sync()

	close(t.done)
	t.running = false
	t.protoBuf = ""

	if t.restore != nil {
		t.restore()
		t.restore = nil
	}

	// Terminate the readLoop before returning so no stale goroutine is left
	// reading stdin while a successor engine (the /setup wizard, or the app
	// relaunched after it) starts its own reader. Two readers on os.Stdin
	// race for every keystroke; input routed to the dead engine is silently
	// lost and the wizard appears frozen.
	t.shutdownReadLoop()
}

// readLoopShutdownTimeout bounds how long Stop waits for the readLoop to
// observe done and exit. The interrupt normally wakes it in microseconds;
// the bound only matters when a console read cannot be interrupted.
const readLoopShutdownTimeout = 250 * time.Millisecond

// shutdownReadLoop actively terminates the stdin readLoop so the NEXT TUI
// engine (setup wizard, relaunched app) starts with exactly one reader on
// stdin. It interrupts a read blocked in t.reader.Read — a read deadline on
// pollable readers (os.Stdin on Unix, os.Pipe anywhere), CancelIoEx on
// Windows consoles — then waits (bounded) for the loop to exit. Without
// this, the stale readLoop and the new engine's readLoop race for input and
// the wizard GUI appears frozen (keys consumed by the dead engine).
func (t *ProcessTerminal) shutdownReadLoop() {
	if t.readLoopDone == nil {
		return
	}
	t.interruptStdinRead()
	select {
	case <-t.readLoopDone:
	case <-time.After(readLoopShutdownTimeout):
	}
	t.clearStdinReadInterrupt()
}

// drainInput reads any pending stdin data until idle for idleMs or maxMs reached.
// Prevents buffered key sequences from leaking to the parent shell after exit.
// The platform-specific implementation avoids leaking goroutines by using
// non-blocking I/O where available.
func (t *ProcessTerminal) drainInput(maxMs, idleMs int) {
	drainInputNonBlocking(t.fd, maxMs, idleMs)
}

// termLog is the optional terminal-output capture (GOA_TERM_LOG). When set,
// every byte written to stdout is also appended to the file — the diagnostic
// for rendering bugs that only reproduce on a real terminal (bugs.md
// Issue 20: escape-sequence semantics the byte-level harness cannot
// arbitrate). Lazily opened on first write; append so relaunches keep history.
var termLog struct {
	once sync.Once
	file *os.File
}

func termLogWriter() *os.File {
	// Check the env first so the once only fires when the feature is enabled
	// (an early write with GOA_TERM_LOG unset must not latch a nil file for
	// the rest of the process).
	path := os.Getenv("GOA_TERM_LOG")
	if path == "" {
		return nil
	}
	termLog.once.Do(func() {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
		if err == nil {
			termLog.file = f
		}
	})
	return termLog.file
}

// Write writes bytes to stdout.
func (t *ProcessTerminal) Write(p []byte) (n int, err error) {
	if f := termLogWriter(); f != nil {
		_, _ = f.Write(p)
	}
	return os.Stdout.Write(p)
}

// WriteString writes a string.
func (t *ProcessTerminal) WriteString(s string) {
	if f := termLogWriter(); f != nil {
		_, _ = f.WriteString(s)
	}
	os.Stdout.WriteString(s)
}

// Size returns terminal dimensions.
//
// Transient-filtered (bugs.md "Mascot redraw"): a failed or degenerate
// TIOCGWINSZ read does NOT fall back to the 80x24 default once a plausible
// size is known — that single-frame 80x24 blip was the source of the
// mid-session header repaint. Instead the last known-good size is returned
// until a real, plausible size is read again. Genuine resizes still pass
// through immediately (they read as valid, non-degenerate sizes).
func (t *ProcessTerminal) Size() (width, height int) {
	w, h, err := term.GetSize(t.fd)
	return t.filteredSize(w, h, err != nil)
}

// filteredSize applies the transient filter to a raw ioctl result. It is the
// testable decision point: GetSize itself cannot be mocked (fixed fd), but
// the accept/cache/fallback logic can. readErr reports the ioctl failed.
func (t *ProcessTerminal) filteredSize(w, h int, readErr bool) (int, int) {
	t.sizeMu.Lock()
	defer t.sizeMu.Unlock()

	// A valid, non-degenerate read: cache and return it. Degenerate means
	// below the minimum the UI can lay out in (the compositor itself clamps
	// height<10 to 24) — a real terminal is never 0-2 rows mid-session.
	if !readErr && w >= 10 && h >= 3 {
		t.lastGoodW = w
		t.lastGoodH = h
		return w, h
	}

	// Transient failure or degenerate read: return the last known-good size
	// if we have one, else the historical default.
	if t.lastGoodW > 0 && t.lastGoodH > 0 {
		return t.lastGoodW, t.lastGoodH
	}
	if w < 10 {
		w = 80
	}
	if h < 3 {
		h = 24
	}
	return w, h
}

// SetRaw enters raw mode.
func (t *ProcessTerminal) SetRaw() (restore func(), err error) {
	fd := int(os.Stdin.Fd())
	oldState, err := term.MakeRaw(fd)
	if err != nil {
		return nil, err
	}
	return func() { term.Restore(fd, oldState) }, nil
}

// HideCursor hides the cursor.
func (t *ProcessTerminal) HideCursor() { os.Stdout.WriteString("\x1b[?25l") }

// ShowCursor shows the cursor.
func (t *ProcessTerminal) ShowCursor() { os.Stdout.WriteString("\x1b[?25h") }

// ClearScreen clears the screen.
func (t *ProcessTerminal) ClearScreen() { os.Stdout.WriteString("\x1b[2J\x1b[H") }

// SetTitle sets the terminal window title via OSC 0 escape sequence.
func (t *ProcessTerminal) SetTitle(title string) {
	os.Stdout.WriteString("\x1b]0;" + title + "\x07")
}
