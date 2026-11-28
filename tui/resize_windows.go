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
	resizeSafetyNetInterval = 150 * time.Millisecond

	// peekBatchSize bounds how many head records a single peek inspects. A
	// console resize coalesces to one event at a time, so the resize record
	// is the queue tail whenever it is present; 32 covers pathological
	// bursts without meaningful cost (~640 bytes on the stack).
	peekBatchSize = 32
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
// corrupt the record stream returned by PeekConsoleInput.
var (
	_ [20 - unsafe.Sizeof(consoleInputRecord{})]byte
	_ [unsafe.Sizeof(consoleInputRecord{}) - 20]byte
)

// Console API entry points absent from golang.org/x/sys/windows v0.47.0.
var (
	kernel32DLL          = windows.NewLazySystemDLL("kernel32.dll")
	procPeekConsoleInput = kernel32DLL.NewProc("PeekConsoleInputW")
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
// ReadFile (os.Stdin.Read), so this watcher is strictly peek-only: it never
// calls ReadConsoleInputW (which would block on an empty queue or dequeue a
// foreign record) and never writes records back (which would reorder keys).
// The handle wake-up only signals "input exists"; the watcher peeks at the
// queued records to detect a resize event, then re-reads the console size —
// a shared-reader race can only ever cost one 150ms safety-net interval of
// latency, never a lost, reordered, or blocked key.
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
			// Input became available. Peek-only: if a buffer-size record is
			// queued, re-read the console size; foreign records belong to the
			// stdin byte reader and are never dequeued.
			if ok := w.checkResizeRecords(out); !ok {
				w.degradeToPolling(out, done)
				return
			}
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

// checkResizeRecords reports whether the console input queue currently holds
// a buffer-size record and, when it does, refreshes the size baseline (which
// emits a notification on change). Records are only ever peeked, never
// dequeued; false signals a console API failure that degrades the watcher to
// polling.
func (w *winResizeWatcher) checkResizeRecords(out chan<- struct{}) bool {
	seen, ok := w.peekResizeRecords()
	if !ok {
		return false
	}
	if seen {
		w.notifyIfResized(out)
	}
	return true
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

// peekResizeRecords reports whether any queued input record is a
// WINDOW_BUFFER_SIZE_EVENT. The peek never blocks: an empty (or concurrently
// drained) queue simply reports "no resize seen". ok=false signals a console
// API failure.
func (w *winResizeWatcher) peekResizeRecords() (seen, ok bool) {
	var buf [peekBatchSize]consoleInputRecord
	var n uint32
	r, _, _ := procPeekConsoleInput.Call(
		uintptr(w.h),
		uintptr(unsafe.Pointer(&buf[0])),
		uintptr(len(buf)),
		uintptr(unsafe.Pointer(&n)),
	)
	if r == 0 {
		return false, false
	}
	for i := uint32(0); i < n; i++ {
		if isResizeRecord(inputRecord{eventType: buf[i].eventType}) {
			return true, true
		}
	}
	return false, true
}
