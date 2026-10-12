// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build unix

package app

import (
	"syscall"
	"testing"
	"time"
)

// TestShutdownSignals_FirstSignalCancelsContext asserts the console signal
// reaches the session as a cancelled context. The second signal escalates to a
// hard exit, which cannot be asserted in-process without killing the test run —
// covered instead by the PTY e2e (cmd/goa/e2e_server_signal_test.go).
//
// Unix-only: syscall.Kill/syscall.Getpid do not exist on Windows, and Windows
// os.Process.Signal cannot deliver SIGTERM anyway.
func TestShutdownSignals_FirstSignalCancelsContext(t *testing.T) {
	ctx, release := shutdownSignals()
	defer release()

	if err := syscall.Kill(syscall.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatalf("send SIGTERM: %v", err)
	}

	select {
	case <-ctx.Done():
	case <-time.After(5 * time.Second):
		t.Fatal("first SIGTERM did not cancel the shutdown context")
	}
}
