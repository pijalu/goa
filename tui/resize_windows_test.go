//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// x/sys/windows does not expose the console screen-buffer setters; load them
// from kernel32 directly (thin wrappers around the Win32 API).
var (
	procSetConsoleScreenBufferSize = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleScreenBufferSize")
	procSetConsoleWindowInfo       = windows.NewLazySystemDLL("kernel32.dll").NewProc("SetConsoleWindowInfo")
)

func setConsoleScreenBufferSize(h windows.Handle, size windows.Coord) error {
	r1, _, e1 := procSetConsoleScreenBufferSize.Call(uintptr(h), uintptr(*(*uint32)(unsafe.Pointer(&size))))
	if r1 == 0 {
		return e1
	}
	return nil
}

func setConsoleWindowInfo(h windows.Handle, absolute bool, rect *windows.SmallRect) error {
	var abs uintptr
	if absolute {
		abs = 1
	}
	r1, _, e1 := procSetConsoleWindowInfo.Call(uintptr(h), abs, uintptr(unsafe.Pointer(rect)))
	if r1 == 0 {
		return e1
	}
	return nil
}

// testConsoleHandle resolves a live console screen-buffer handle for the test:
// a standard handle when one is a console, else the attached console via
// CONOUT$ (reachable even when all standard handles are redirected pipes).
func testConsoleHandle(t *testing.T) (windows.Handle, bool) {
	t.Helper()
	for _, hd := range []windows.Handle{
		windows.Handle(os.Stdout.Fd()),
		windows.Handle(os.Stdin.Fd()),
		windows.Handle(os.Stderr.Fd()),
	} {
		if _, _, ok := consoleWindowSize(hd); ok {
			return hd, true
		}
	}
	hd, err := conOutHandle()
	if err != nil {
		return 0, false
	}
	if _, _, ok := consoleWindowSize(hd); ok {
		return hd, true
	}
	return 0, false
}

// TestResizeEvents_FiresOnConsoleResize resizes a REAL console and verifies
// the Windows resize poller (resize_windows.go) emits an event and reports
// the new size. This is the regression test for "goa's screen never adapts to
// the console width on Windows": if the poller does not observe a genuine
// console size change (or cannot read one because its size source is pinned
// to a redirected stdin), the TUI never re-renders at the new size and stays
// stuck at the launch width forever.
func TestResizeEvents_FiresOnConsoleResize(t *testing.T) {
	h, ok := testConsoleHandle(t)
	if !ok {
		t.Skip("no attached console (headless service context)")
	}
	winW, winH, ok := consoleWindowSize(h)
	if !ok {
		t.Skip("console handle lost")
	}
	if winW < 25 || winH < 5 {
		t.Skipf("console too small to resize: %dx%d", winW, winH)
	}

	// Grow the buffer first so the window can expand past the current size;
	// SetConsoleWindowInfo fails when the window would exceed the buffer.
	origInfo := new(windows.ConsoleScreenBufferInfo)
	if err := windows.GetConsoleScreenBufferInfo(h, origInfo); err != nil {
		t.Fatalf("GetConsoleScreenBufferInfo: %v", err)
	}
	defer func() {
		// Restore the original window and buffer size for the host console
		// (best effort; a restore failure must not fail the test).
		buf := windows.Coord{X: origInfo.Size.X, Y: origInfo.Size.Y}
		_ = setConsoleScreenBufferSize(h, buf)
		_ = setConsoleWindowInfo(h, true, &origInfo.Window)
	}()

	// Start the poller BEFORE resizing: it captures the current size as its
	// baseline, so a change AFTER this point must produce an event.
	done := make(chan struct{})
	defer close(done)
	events := resizeEvents(done)

	newW := winW + 8
	newH := winH + 3
	buf := windows.Coord{X: int16(newW + 16), Y: int16(newH + 16)}
	if err := setConsoleScreenBufferSize(h, buf); err == nil {
		rect := windows.SmallRect{Left: 0, Top: 0, Right: int16(newW - 1), Bottom: int16(newH - 1)}
		if err := setConsoleWindowInfo(h, true, &rect); err != nil {
			t.Fatalf("SetConsoleWindowInfo(grow): %v", err)
		}
	} else {
		// Buffer growth rejected (e.g. the host is a conpty/Windows Terminal
		// pseudoconsole, which owns buffer sizing): shrink the WINDOW instead —
		// always legal without touching the buffer, and the size-change signal
		// is identical from the poller's point of view.
		t.Logf("SetConsoleScreenBufferSize rejected (%v); shrinking window instead", err)
		newW = winW - 6
		newH = winH - 3
		if newW < 10 || newH < 3 {
			t.Skipf("console too small to shrink: %dx%d", winW, winH)
		}
		rect := windows.SmallRect{Left: 0, Top: 0, Right: int16(newW - 1), Bottom: int16(newH - 1)}
		if err := setConsoleWindowInfo(h, true, &rect); err != nil {
			t.Skipf("SetConsoleWindowInfo(shrink): %v", err)
		}
	}

	select {
	case <-events:
		// Poller observed the resize.
	case <-time.After(3 * time.Second):
		t.Fatalf("resizeEvents did not fire after console resized to %dx%d", newW, newH)
	}

	// The poller must also report the NEW size via consoleSize() so the TUI
	// re-renders at the new width.
	cw, ch := consoleSize()
	if cw != newW || ch != newH {
		t.Fatalf("consoleSize() = %dx%d after resize, want %dx%d", cw, ch, newW, newH)
	}

	// queryConsoleSize — the size source ProcessTerminal.Size now uses — must
	// agree with the poller.
	qw, qh, ok := queryConsoleSize()
	if !ok || qw != newW || qh != newH {
		t.Fatalf("queryConsoleSize() = %dx%d ok=%v after resize, want %dx%d", qw, qh, ok, newW, newH)
	}
}
