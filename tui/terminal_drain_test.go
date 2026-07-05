// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"io"
	"testing"
	"time"
)

// TestDrainInputFallback_ExitsOnEOF verifies that the fallback drain loop
// terminates immediately when stdin reaches EOF.
func TestDrainInputFallback_ExitsOnEOF(t *testing.T) {
	oldRead := stdinRead
	oldFile := stdinFile
	defer func() {
		stdinRead = oldRead
		stdinFile = oldFile
	}()

	stdinFile = nil
	stdinRead = func(buf []byte) (int, error) {
		return 0, io.EOF
	}

	done := make(chan struct{})
	go func() {
		drainInputFallback(100, 10)
		close(done)
	}()

	select {
	case <-done:
		// ok
	case <-time.After(time.Second):
		t.Fatal("drainInputFallback did not exit on EOF")
	}
}

// TestDrainInputFallback_ExitsWhenIdle verifies that the fallback drain loop
// exits after the idle window when no further data arrives.
func TestDrainInputFallback_ExitsWhenIdle(t *testing.T) {
	oldRead := stdinRead
	oldFile := stdinFile
	defer func() {
		stdinRead = oldRead
		stdinFile = oldFile
	}()

	stdinFile = nil
	readCalls := make(chan int, 1)
	unblock := make(chan struct{})
	stdinRead = func(buf []byte) (int, error) {
		readCalls <- 1
		<-unblock
		return 0, io.EOF
	}

	done := make(chan struct{})
	go func() {
		drainInputFallback(1000, 20)
		close(done)
	}()

	// Wait for at least one read call so the drain loop is active.
	select {
	case <-readCalls:
	case <-time.After(time.Second):
		t.Fatal("drainInputFallback did not start reading")
	}

	// The drain loop should exit after the idle window (20ms) elapses.
	select {
	case <-done:
		close(unblock)
	case <-time.After(time.Second):
		close(unblock)
		t.Fatal("drainInputFallback did not exit after idle window")
	}
}
