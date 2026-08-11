//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"time"
)

// resizeEvents returns a channel that fires when the terminal size changes.
// Windows has no SIGWINCH equivalent, so the console size is polled. The poller
// stops when done is closed.
func resizeEvents(done <-chan struct{}) <-chan struct{} {
	out := make(chan struct{}, 4)
	lastW, lastH := consoleSize()
	go func() {
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-done:
				return
			case <-ticker.C:
				w, h := consoleSize()
				if w != lastW || h != lastH {
					lastW, lastH = w, h
					select {
					case out <- struct{}{}:
					default:
					}
				}
			}
		}
	}()
	return out
}

// consoleSize returns the current console dimensions via queryConsoleSize
// (the same robust console-handle resolution ProcessTerminal.Size uses, so
// the poller and the renderer always agree on the size source). On failure it
// falls back to the historical 80x24 default.
func consoleSize() (int, int) {
	if w, h, ok := queryConsoleSize(); ok {
		return w, h
	}
	return 80, 24
}
