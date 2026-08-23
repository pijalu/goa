//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	// resizeWaitSlice bounds each WaitForSingleObject call so the watcher
	// re-checks done periodically and runs the safety-net size check without
	// needing a dedicated wake-up event object.
	resizeWaitSlice = 50 * time.Millisecond

	// resizeSafetyNetInterval bounds how long a missed WINDOW_BUFFER_SIZE
	// event can delay a resize notification (e.g. when the stdin byte reader
	// wins the race for the record). One GetConsoleScreenBufferInfo per
	// interval is negligible next to the old unconditional 250ms polling.
	resizeSafetyNetInterval = time.Second

	// maxConsumeSpins caps the drain loop so a pathological interleaving with
	// a concurrent reader cannot spin forever; the safety net covers anything
	// left unconsumed.
	maxConsumeSpins = 128
)

// consoleInputRecord mirrors the Win32 INPUT_RECORD layout (wincon.h): a WORD
// EventType followed by an at-least-4-byte-aligned union payload whose largest
// members (KEY_EVENT_RECORD, MOUSE_EVENT_RECORD) are 16 bytes — 20 bytes total.
// Only EventType is interpreted. golang.org/x/sys/windows does not define
// INPUT_RECORD, hence this local mirror.
type consoleInputRecord struct {
	eventType uint16
	_         [2]byte  // alignment padding before the union
	payload   [16]byte // opaque event union
}

// Compile-time layout guards: any drift from the documented 20-byte ABI would
// corrupt the record stream returned by ReadConsoleInput/WriteConsoleInput.
var (
	_ [20 - unsafe.Sizeof(consoleInputRecord{})]byte
	_ [unsafe.Sizeof(consoleInputRecord{}) - 20]byte
)

// Console API entry points absent from golang.org/x/sys/windows v0.47.0.
var (
	kernel32DLL           = windows.NewLazySystemDLL("kernel32.dll")
	procPeekConsoleInput  = kernel32DLL.NewProc("PeekConsoleInputW")
	procReadConsoleInput  = kernel32DLL.NewProc("ReadConsoleInputW")
	procWriteConsoleInput = kernel32DLL.NewProc("WriteConsoleInputW")
)

// newPlatformResizeWatcher returns the console-event watcher, or nil when
// stdin is not a console handle (event mode unavailable → polling fallback).
// A fresh instance is created per resizeEvents call: the watcher carries
// per-session state (baseline size, mode restore) and must never be shared.
func newPlatformResizeWatcher() resizeWatcher {
	var mode uint32
	if err := windows.GetConsoleMode(windows.Handle(os.Stdin.Fd()), &mode); err != nil {
		return nil // stdin is not a console handle
	}
	return &winResizeWatcher{}
}

// winResizeWatcher watches the Windows console input queue for
// WINDOW_BUFFER_SIZE_EVENT records instead of polling the buffer geometry.
//
// The TUI's stdin byte reader consumes the same console input queue via
// ReadFile (os.Stdin.Read), so this watcher is deliberately conservative:
// it peeks at the head record and only dequeues records it owns (buffer-size
// events). Key/mouse/focus records are left untouched for the byte reader;
// if a concurrent read steals our peeked record between Peek and Read, the
// foreign record we get back is written back to the queue rather than
// swallowed. A one-second safety-net poll bounds the damage of any missed
// event.
type winResizeWatcher struct {
	h               windows.Handle
	restore         func() // clears only the flag we added
	lastW, lastH    int    // last notified size (baseline)
	nextSafetyCheck time.Time
}

// watch emits into out on every console resize until done is closed,
// degrading to the legacy 250ms poller when console APIs fail mid-flight.
func (w *winResizeWatcher) watch(out chan<- struct{}, done <-chan struct{}) {
	if !w.enableWindowInputEvents() {
		runResizePoller(out, done, consoleSizeFunc, resizeFallbackInterval)
		return
	}
	defer w.restoreMode()
	w.lastW, w.lastH = consoleSizeFunc()
	w.nextSafetyCheck = time.Now().Add(resizeSafetyNetInterval)
	for {
		select {
		case <-done:
			return
		default:
		}
		switch ev, err := windows.WaitForSingleObject(w.h, uint32(resizeWaitSlice/time.Millisecond)); {
		case err != nil:
			w.degradeToPolling(out, done)
			return
		case ev == windows.WAIT_OBJECT_0:
			// Input became available: consume resize records we own, then
			// refresh the baseline so key-event wake-ups stay cheap.
			if !w.consumeResizeRecords(out) {
				w.degradeToPolling(out, done)
				return
			}
			w.notifyIfResized(out)
		default: // WAIT_TIMEOUT: idle slice
			w.safetyNet(out)
		}
	}
}

// degradeToPolling hands over to the fallback poller after restoring the
// console mode state that event mode modified.
func (w *winResizeWatcher) degradeToPolling(out chan<- struct{}, done <-chan struct{}) {
	w.restoreMode()
	runResizePoller(out, done, consoleSizeFunc, resizeFallbackInterval)
}

// enableWindowInputEvents turns on ENABLE_WINDOW_INPUT via read-modify-write,
// preserving every flag set by other components (raw mode, VT input, ...).
func (w *winResizeWatcher) enableWindowInputEvents() bool {
	w.h = windows.Handle(os.Stdin.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(w.h, &mode); err != nil {
		return false
	}
	if err := windows.SetConsoleMode(w.h, mode|windows.ENABLE_WINDOW_INPUT); err != nil {
		return false // flag rejected: event mode unavailable
	}
	w.restore = w.clearWindowInputFlag
	return true
}

// clearWindowInputFlag removes only the bit this watcher added. It re-reads
// the current mode instead of restoring a stale snapshot so flags changed by
// other owners while the watcher ran are preserved.
func (w *winResizeWatcher) clearWindowInputFlag() {
	var mode uint32
	if err := windows.GetConsoleMode(w.h, &mode); err != nil {
		return
	}
	_ = windows.SetConsoleMode(w.h, mode&^windows.ENABLE_WINDOW_INPUT)
}

// restoreMode is safe to call even when event mode was never enabled.
func (w *winResizeWatcher) restoreMode() {
	if w.restore != nil {
		w.restore()
	}
}

// consumeResizeRecords drains WINDOW_BUFFER_SIZE_EVENT records from the head
// of the input queue, firing out for each. The queue is only touched while
// its head record belongs to us; reports false on console API failure so the
// caller can fall back to polling.
func (w *winResizeWatcher) consumeResizeRecords(out chan<- struct{}) bool {
	for spin := 0; spin < maxConsumeSpins; spin++ {
		keep, resized, ok := w.drainStep()
		if resized {
			sendResize(out)
		}
		if !ok {
			return false
		}
		if !keep {
			return true
		}
	}
	return true
}

// drainStep performs one consumption decision cycle. keep asks the caller to
// continue draining (an owned record was just consumed), resized reports that
// the consumed record was a buffer-size event, and ok=false signals a console
// API failure that should degrade the watcher to polling.
func (w *winResizeWatcher) drainStep() (keep, resized, ok bool) {
	pending, ok := w.pendingCount()
	if !ok || pending == 0 {
		return false, false, ok
	}
	head, got, ok := w.peekHead()
	if !ok {
		return false, false, false
	}
	if !got || head.eventType != winEventWindowBufferSize {
		// Queue drained concurrently, or the head belongs to another
		// consumer (the stdin byte reader): leave it untouched.
		return false, false, true
	}
	rec, got, ok := w.readOne()
	if !ok {
		return false, false, false
	}
	if !got {
		return false, false, true // consumed concurrently between peek and read
	}
	if isResizeRecord(inputRecord{eventType: rec.eventType}) {
		return true, true, true
	}
	// Lost the race with a concurrent reader that dequeued our peeked
	// record. Never swallow foreign events — write it back and stop; the
	// next handle signal re-enters the drain if more records arrive.
	if !w.reinject(rec) {
		return false, false, false // queue unusable: degrade to polling
	}
	return true, false, true
}

// notifyIfResized compares the current console size against the baseline and
// emits exactly one notification per actual change.
func (w *winResizeWatcher) notifyIfResized(out chan<- struct{}) {
	curW, curH := consoleSizeFunc()
	if curW != w.lastW || curH != w.lastH {
		w.lastW, w.lastH = curW, curH
		sendResize(out)
	}
}

// safetyNet re-checks the size at most once per resizeSafetyNetInterval,
// covering events lost to races with other readers of the input queue.
func (w *winResizeWatcher) safetyNet(out chan<- struct{}) {
	now := time.Now()
	if now.Before(w.nextSafetyCheck) {
		return
	}
	w.nextSafetyCheck = now.Add(resizeSafetyNetInterval)
	w.notifyIfResized(out)
}

// pendingCount reports how many input records are queued.
func (w *winResizeWatcher) pendingCount() (uint32, bool) {
	var n uint32
	if err := windows.GetNumberOfConsoleInputEvents(w.h, &n); err != nil {
		return 0, false
	}
	return n, true
}

// peekHead inspects the head record without dequeuing it. ok=false signals a
// console API failure; got=false means the queue emptied concurrently.
func (w *winResizeWatcher) peekHead() (rec consoleInputRecord, got, ok bool) {
	buf := make([]consoleInputRecord, 1)
	var read uint32
	r, _, _ := procPeekConsoleInput.Call(
		uintptr(w.h),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&read)),
	)
	if r == 0 {
		return consoleInputRecord{}, false, false
	}
	if read == 0 {
		return consoleInputRecord{}, false, true
	}
	return buf[0], true, true
}

// readOne dequeues exactly one record. ok=false signals a console API
// failure; got=false means nothing was read (queue emptied concurrently).
func (w *winResizeWatcher) readOne() (rec consoleInputRecord, got, ok bool) {
	buf := make([]consoleInputRecord, 1)
	var read uint32
	r, _, _ := procReadConsoleInput.Call(
		uintptr(w.h),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&read)),
	)
	if r == 0 {
		return consoleInputRecord{}, false, false
	}
	if read == 0 {
		return consoleInputRecord{}, false, true
	}
	return buf[0], true, true
}

// reinject writes a previously dequeued record back to the end of the input
// queue so the owning consumer (stdin byte reader) still receives it. Used
// only in the rare race where the record changed identity between Peek and
// Read; ordering distortion under that race is accepted and bounded.
func (w *winResizeWatcher) reinject(rec consoleInputRecord) bool {
	var written uint32
	r, _, _ := procWriteConsoleInput.Call(
		uintptr(w.h),
		uintptr(unsafe.Pointer(&rec)),
		1,
		uintptr(unsafe.Pointer(&written)),
	)
	return r != 0 && written == 1
}
