//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"sync"

	"golang.org/x/sys/windows"
)

// queryConsoleSize returns the attached console's WINDOW size via
// GetConsoleScreenBufferInfo. Windows console handles are resolved robustly:
// the standard handles (stdout, stdin, stderr) are tried first — in a normal
// interactive run at least one of them IS the console — and a cached CONOUT$
// handle (the process's attached console screen buffer, reachable even when
// every standard handle is redirected to a pipe) is the final fallback.
//
// This is the root-cause fix for "the goa screen never adapts to the console
// width on Windows": both ProcessTerminal.Size and the resize poller used to
// query os.Stdin only. When stdin is a pipe (goa < file, a GUI/IDE launcher,
// a wrapper that redirects input), GetConsoleScreenBufferInfo(stdin) fails and
// every frame falls back to the 80x24 default — and, because the poller also
// read the same constant, it never detected a resize, so the screen stayed at
// 80 columns regardless of the actual console width.
func queryConsoleSize() (w, h int, ok bool) {
	for _, hd := range []windows.Handle{
		windows.Handle(os.Stdout.Fd()),
		windows.Handle(os.Stdin.Fd()),
		windows.Handle(os.Stderr.Fd()),
	} {
		if w, h, ok := consoleWindowSize(hd); ok {
			return w, h, true
		}
	}
	if hd, err := conOutHandle(); err == nil {
		if w, h, ok := consoleWindowSize(hd); ok {
			return w, h, true
		}
	}
	return 0, 0, false
}

// consoleWindowSize returns the console WINDOW size for a handle, or ok=false
// when the handle is not a console screen buffer (e.g. a redirected pipe).
func consoleWindowSize(hd windows.Handle) (w, h int, ok bool) {
	var info windows.ConsoleScreenBufferInfo
	if err := windows.GetConsoleScreenBufferInfo(hd, &info); err != nil {
		return 0, 0, false
	}
	ww := int(info.Window.Right-info.Window.Left) + 1
	hh := int(info.Window.Bottom-info.Window.Top) + 1
	if ww <= 0 || hh <= 0 {
		return 0, 0, false
	}
	return ww, hh, true
}

// conOutHandle opens the process's attached console screen buffer via
// "CONOUT$" and caches the handle (a console handle outlives its opening call;
// it stays valid for the process lifetime). This reaches the console even when
// all three standard handles are redirected — the headless-launcher case.
var conOutOnce struct {
	sync.Once
	hd  windows.Handle
	err error
}

func conOutHandle() (windows.Handle, error) {
	conOutOnce.Do(func() {
		p, err := windows.UTF16PtrFromString("CONOUT$")
		if err != nil {
			conOutOnce.err = err
			return
		}
		hd, err := windows.CreateFile(p,
			windows.GENERIC_READ|windows.GENERIC_WRITE,
			windows.FILE_SHARE_READ|windows.FILE_SHARE_WRITE,
			nil, windows.OPEN_EXISTING, 0, 0)
		if err != nil {
			conOutOnce.err = err
			return
		}
		conOutOnce.hd = hd
	})
	return conOutOnce.hd, conOutOnce.err
}

// readSize returns the console window size via GetConsoleScreenBufferInfo.
// The size is NOT read from t.fd (stdin) directly: on Windows stdin may be a
// redirected pipe even when the app has an attached console, and a pipe handle
// cannot report a console size. queryConsoleSize resolves a real console
// handle (stdout → stdin → stderr → CONOUT$), keeping the frame size and the
// resize poller on the same, robust source.
func (t *ProcessTerminal) readSize() (w, h int, err error) {
	if w, h, ok := queryConsoleSize(); ok {
		return w, h, nil
	}
	return 0, 0, os.ErrInvalid
}
