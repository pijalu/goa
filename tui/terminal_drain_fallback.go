// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"
	"time"
)

// drainInputFallback polls stdin using a detached blocking-read goroutine.
// It is used on platforms where non-blocking terminal reads are unavailable
// (Windows) or when setting non-blocking mode fails. The goroutine exits
// naturally when the process terminates or stdin is closed.
func drainInputFallback(maxMs, idleMs int) {
	deadline := time.After(time.Duration(maxMs) * time.Millisecond)
	idle := time.NewTimer(time.Duration(idleMs) * time.Millisecond)
	defer idle.Stop()

	ch := startStdinPoller()

	for {
		select {
		case r := <-ch:
			if r.err != nil || r.n == 0 {
				return
			}
			idle.Reset(time.Duration(idleMs) * time.Millisecond)
		case <-idle.C:
			return
		case <-deadline:
			return
		}
	}
}

// stdinReadResult carries one read from stdin.
type stdinReadResult struct {
	n   int
	err error
}

// startStdinPoller starts a goroutine that reads from stdin and sends results
// to the returned channel. The channel is closed when stdin reaches EOF or an
// error occurs.
func startStdinPoller() <-chan stdinReadResult {
	ch := make(chan stdinReadResult, 1)
	go func() {
		buf := make([]byte, 256)
		for {
			n, err := stdinRead(buf)
			if err != nil || n == 0 {
				ch <- stdinReadResult{0, err}
				close(ch)
				return
			}
			select {
			case ch <- stdinReadResult{n, nil}:
			default:
			}
		}
	}()
	return ch
}
var stdinRead = defaultStdinRead

func defaultStdinRead(buf []byte) (int, error) {
	return stdinFile.Read(buf)
}

// stdinFile is the standard input file handle.
var stdinFile = os.Stdin
