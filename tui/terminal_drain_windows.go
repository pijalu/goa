// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build windows

package tui

// drainInputNonBlocking on Windows falls back to a blocking read in a
// detached goroutine. Windows consoles do not support the same non-blocking
// read model as POSIX terminals, and the goroutine exits naturally when the
// process terminates or stdin is closed.
func drainInputNonBlocking(fd, maxMs, idleMs int) {
	_ = fd
	go drainInputFallback(maxMs, idleMs)
}
