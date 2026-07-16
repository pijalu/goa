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

// ProcessTerminal implements Terminal with raw mode and Kitty keyboard protocol.
type ProcessTerminal struct {
	fd       int
	onInput  func(string)
	onResize func()
	restore  func()
	running  bool
	done     chan struct{}

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
		t.stdinBuffer = NewStdinBuffer()

		// Enable Windows VT input (no-op on other platforms)
		enableWindowsVTInput()

		// Enable bracketed paste mode
		os.Stdout.WriteString("\x1b[?2004h")

		// Query Kitty keyboard protocol
		t.queryKitty()
	}

	// Always start the read loop — even without raw mode, this allows
	// processing commands from pipes and non-terminal stdin.
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

		n, err := os.Stdin.Read(buf)
		if err != nil {
			return
		}
		if n == 0 {
			continue
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
	_ = setStdinReadDeadline(time.Now().Add(escapeDebounceTimeout))

	buf := make([]byte, 256)
	n, err := os.Stdin.Read(buf)

	// Cancel the pending debounce regardless of outcome.
	t.escapeMu.Lock()
	t.escapePending.Store(false)
	if t.escapeTimer != nil {
		t.escapeTimer.Stop()
		t.escapeTimer = nil
	}
	t.escapeMu.Unlock()

	// Clear the read deadline so subsequent reads block normally.
	_ = setStdinReadDeadline(time.Time{})

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

// setStdinReadDeadline sets the read deadline on stdin (Unix only).
// A zero time.Time clears the deadline.
func setStdinReadDeadline(t time.Time) error {
	return os.Stdin.SetReadDeadline(t)
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
	if !t.running {
		// Already stopped or raw mode was never entered. Still attempt a
		// terminal restore in case Stop is called after a partial startup.
		if t.restore != nil {
			t.restore()
			t.restore = nil
		}
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
}

// drainInput reads any pending stdin data until idle for idleMs or maxMs reached.
// Prevents buffered key sequences from leaking to the parent shell after exit.
// The platform-specific implementation avoids leaking goroutines by using
// non-blocking I/O where available.
func (t *ProcessTerminal) drainInput(maxMs, idleMs int) {
	drainInputNonBlocking(t.fd, maxMs, idleMs)
}

// Write writes bytes to stdout.
func (t *ProcessTerminal) Write(p []byte) (n int, err error) { return os.Stdout.Write(p) }

// WriteString writes a string.
func (t *ProcessTerminal) WriteString(s string) { os.Stdout.WriteString(s) }

// Size returns terminal dimensions.
func (t *ProcessTerminal) Size() (width, height int) {
	w, h, err := term.GetSize(t.fd)
	if err != nil {
		return 80, 24
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
