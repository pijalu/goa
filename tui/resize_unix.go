//go:build !windows

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"os/signal"
	"syscall"
)

// sigwinchWatcher delivers terminal resizes via SIGWINCH, the native resize
// signal on Unix-like systems. Behavior is identical to the pre-abstraction
// implementation: a buffered signal channel feeding sendResize, stopped via
// signal.Stop when done closes.
type sigwinchWatcher struct{}

// newPlatformResizeWatcher returns the SIGWINCH watcher; signals are always
// available on Unix, so this never requests polling fallback.
func newPlatformResizeWatcher() resizeWatcher {
	return sigwinchWatcher{}
}

// watch emits into out on every SIGWINCH until done is closed.
func (sigwinchWatcher) watch(out chan<- struct{}, done <-chan struct{}) {
	sig := make(chan os.Signal, resizeChannelBuffer)
	signal.Notify(sig, syscall.SIGWINCH)
	defer signal.Stop(sig)
	for {
		select {
		case <-sig:
			sendResize(out)
		case <-done:
			return
		}
	}
}
