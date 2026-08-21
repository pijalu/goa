// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build windows

package tui

import (
	"time"

	"golang.org/x/sys/windows"
)

// interruptStdinRead wakes a read blocked in t.reader.Read so the readLoop
// can observe the closed done channel and exit.
//
// Two cases:
//   - Pollable readers (os.Pipe, including test harnesses) honor a read
//     deadline, which makes the blocked read return immediately.
//   - A real console handle does not support deadlines (os.Stdin.Read goes
//     through ReadConsole, which is not poller-backed): cancel the pending
//     read directly with CancelIoEx. This is what lets the previous engine's
//     readLoop terminate before the setup wizard starts its own reader.
func (t *ProcessTerminal) interruptStdinRead() {
	if err := setReadDeadline(t.reader, time.Now()); err == nil {
		return
	}
	_ = windows.CancelIoEx(windows.Handle(t.fd), nil)
}

// clearStdinReadInterrupt removes the interrupt deadline on pollable readers
// so the NEXT engine's reads block normally again.
func (t *ProcessTerminal) clearStdinReadInterrupt() {
	_ = setReadDeadline(t.reader, time.Time{})
}
