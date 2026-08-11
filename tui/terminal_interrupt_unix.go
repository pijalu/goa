// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build !windows

package tui

import "time"

// interruptStdinRead wakes a read blocked in t.reader.Read so the readLoop
// can observe the closed done channel and exit. On Unix, os.Stdin is
// poller-backed and honors a read deadline, so setting a deadline in the
// past makes the blocked read return a timeout immediately.
func (t *ProcessTerminal) interruptStdinRead() {
	_ = setReadDeadline(t.reader, time.Now())
}

// clearStdinReadInterrupt removes the interrupt deadline so the NEXT engine's
// reads on the same underlying stream block normally again. The deadline is
// process-wide on os.Stdin, so it must always be cleared after shutdown.
func (t *ProcessTerminal) clearStdinReadInterrupt() {
	_ = setReadDeadline(t.reader, time.Time{})
}
