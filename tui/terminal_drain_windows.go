// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build windows

package tui

import (
	"time"

	"golang.org/x/sys/windows"
)

// drainInputNonBlocking discards any input queued in the console input buffer
// for up to maxMs (or until idleMs with no pending input), so buffered key
// sequences do not leak to the parent shell after the terminal is restored.
//
// It is fully synchronous and never starts a reader: it polls the console
// buffer with GetNumberOfConsoleInputEvents and flushes it with
// FlushConsoleInputBuffer, checking the idle/deadline timers between polls.
//
// The previous implementation delegated to a detached blocking-read goroutine
// (drainInputFallback) whose stdin poller kept reading the console FOREVER —
// it never observed the drain's idle/deadline. When /setup stopped the main
// TUI and started the wizard (or the wizard ended and the app relaunched),
// that leaked poller and the new engine's readLoop both blocked on the same
// console and raced for every keystroke; input stolen by the poller was
// silently discarded, making the wizard GUI appear frozen/unresponsive.
func drainInputNonBlocking(fd, maxMs, idleMs int) {
	h := windows.Handle(fd)

	var pending uint32
	if err := windows.GetNumberOfConsoleInputEvents(h, &pending); err != nil {
		// Not a console handle (pipe / redirected stdin): nothing to flush.
		return
	}

	deadline := time.After(time.Duration(maxMs) * time.Millisecond)
	idle := time.NewTimer(time.Duration(idleMs) * time.Millisecond)
	defer idle.Stop()

	for {
		select {
		case <-deadline:
			return
		case <-idle.C:
			return
		default:
		}

		if err := windows.GetNumberOfConsoleInputEvents(h, &pending); err != nil {
			return
		}
		if pending > 0 {
			_ = windows.FlushConsoleInputBuffer(h)
			idle.Reset(time.Duration(idleMs) * time.Millisecond)
			continue
		}

		// Nothing queued: wait a short tick so the idle/deadline timers stay
		// live and Stop() never blocks for the full maxMs on quiet terminals.
		select {
		case <-deadline:
			return
		case <-idle.C:
			return
		case <-time.After(5 * time.Millisecond):
		}
	}
}
