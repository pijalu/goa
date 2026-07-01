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

// resizeEvents returns a channel that fires on terminal resize (SIGWINCH) on
// Unix-like systems. It stops emitting when done is closed.
func resizeEvents(done <-chan struct{}) <-chan struct{} {
	out := make(chan struct{}, 4)
	sig := make(chan os.Signal, 4)
	signal.Notify(sig, syscall.SIGWINCH)
	go func() {
		defer signal.Stop(sig)
		for {
			select {
			case <-sig:
				select {
				case out <- struct{}{}:
				default:
				}
			case <-done:
				return
			}
		}
	}()
	return out
}
