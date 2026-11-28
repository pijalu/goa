// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build windows

package tui

// drainInputNonBlocking on Windows drains pending input synchronously via
// the shared-poller fallback: Windows consoles do not support the POSIX
// non-blocking read model, so the fallback's demand-driven reader is used.
// The drain returns when the drain window ends (idle for idleMs or maxMs
// elapsed); the shared poller then issues no further reads, so no goroutine
// is left consuming stdin when a session restarts in-process (bug W5).
func drainInputNonBlocking(fd, maxMs, idleMs int) {
	_ = fd
	drainInputFallback(maxMs, idleMs)
}
