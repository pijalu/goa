// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build !windows

package tui

import (
	"errors"
	"syscall"
	"time"
)

// drainInputNonBlocking polls stdin until no data arrives for idleMs or
// maxMs elapses. It uses non-blocking reads so no goroutine is left stuck in
// a blocking Read after the function returns.
func drainInputNonBlocking(fd, maxMs, idleMs int) {
	if err := syscall.SetNonblock(fd, true); err != nil {
		go drainInputFallback(maxMs, idleMs)
		return
	}
	defer syscall.SetNonblock(fd, false)

	deadline := time.After(time.Duration(maxMs) * time.Millisecond)
	idle := time.NewTimer(time.Duration(idleMs) * time.Millisecond)
	defer idle.Stop()
	buf := make([]byte, 256)

	for {
		select {
		case <-deadline:
			return
		case <-idle.C:
			return
		default:
		}

		n, err := readNonBlocking(fd, buf, deadline, idleMs)
		if err != nil {
			return
		}
		if n == 0 {
			return
		}
		idle.Reset(time.Duration(idleMs) * time.Millisecond)
	}
}

// readNonBlocking attempts one non-blocking read, handling EAGAIN by waiting
// for the idle window. It returns (0, nil) when no data is currently
// available, (n, nil) when data is read, and a non-nil error on failure.
func readNonBlocking(fd int, buf []byte, deadline <-chan time.Time, idleMs int) (int, error) {
	for {
		n, err := syscall.Read(fd, buf)
		if err == nil {
			return n, nil
		}
		if !errors.Is(err, syscall.EAGAIN) && !errors.Is(err, syscall.EWOULDBLOCK) {
			return 0, err
		}
		select {
		case <-deadline:
			return 0, nil
		case <-time.After(time.Duration(idleMs) * time.Millisecond):
			return 0, nil
		}
	}
}
