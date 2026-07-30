// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build unix

package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestTeeStderr verifies that writes to os.Stderr — the path the Go runtime
// uses for fatal errors — are captured into the crash log file, and that the
// cleanup restores fd 2 so later writes are no longer captured. Serial: this
// mutates process-wide fd 2.
func TestTeeStderr(t *testing.T) {
	f, err := os.Create(filepath.Join(t.TempDir(), "tee.log"))
	if err != nil {
		t.Fatal(err)
	}

	cleanup := teeStderr(f, nil)
	fmt.Fprint(os.Stderr, "tee-marker-line-67890\n")
	cleanup() // restores fd 2, drains the pipe, closes f

	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "tee-marker-line-67890") {
		t.Fatalf("stderr write not captured in crash log:\n%s", data)
	}

	// After cleanup, stderr writes must bypass the (closed) log entirely.
	fmt.Fprint(os.Stderr, "post-cleanup-marker-11111\n")
	data, err = os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "post-cleanup-marker-11111") {
		t.Fatal("stderr write after cleanup still reached the crash log")
	}
}

// TestTeeStderr_GatedWhileTUIOwnsScreen pins the fix for stray fd-level
// writes corrupting the TUI (e.g. macOS libmalloc "MallocStackLogging:
// can't turn off malloc stack logging because it was not enabled", written
// straight to fd 2 by C code): while a full-screen TUI owns the terminal,
// captured stderr must go ONLY to the crash log — never back to the TTY.
// Serial: mutates process-wide fd 2.
func TestTeeStderr_GatedWhileTUIOwnsScreen(t *testing.T) {
	// Point the process's real fd 2 at a pipe we observe: the tee's
	// "original stderr" dup then refers to this pipe, so anything the tee
	// would forward to the terminal becomes readable here.
	origFd, err := unix.Dup(unix.Stderr)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = unix.Dup2(origFd, unix.Stderr)
		_ = unix.Close(origFd)
	}()
	ttyR, ttyW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer ttyR.Close()
	defer ttyW.Close()
	if err := unix.Dup2(int(ttyW.Fd()), unix.Stderr); err != nil {
		t.Fatal(err)
	}

	f, err := os.Create(filepath.Join(t.TempDir(), "tee.log"))
	if err != nil {
		t.Fatal(err)
	}

	tuiOwns := false
	cleanup := teeStderr(f, func() bool { return tuiOwns })

	fmt.Fprint(os.Stderr, "tty-visible-marker-aaaa\n")
	if got := readPipeUntil(t, ttyR, "tty-visible-marker-aaaa"); !strings.Contains(got, "tty-visible-marker-aaaa") {
		t.Fatalf("no TUI active: stderr write must be forwarded to the terminal, got %q", got)
	}

	// TUI takes the screen: forwarding must stop, capture must continue.
	tuiOwns = true
	fmt.Fprint(os.Stderr, "tty-hidden-marker-bbbb\n")

	cleanup() // restores fd 2 (to our pipe), drains the tee into f

	data, err := os.ReadFile(f.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "tty-visible-marker-aaaa") ||
		!strings.Contains(string(data), "tty-hidden-marker-bbbb") {
		t.Fatalf("crash log must capture BOTH gated and ungated writes:\n%s", data)
	}

	// Nothing more may arrive at the terminal side: the only bytes allowed
	// in the pipe are the pre-TUI marker (already drained above).
	_ = ttyR.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	buf := make([]byte, 4096)
	n, _ := ttyR.Read(buf)
	if n > 0 && strings.Contains(string(buf[:n]), "tty-hidden-marker-bbbb") {
		t.Fatalf("stderr write while TUI owned the screen leaked to the terminal: %q", buf[:n])
	}
}

// readPipeUntil reads from r until want appears or the deadline expires,
// returning everything read. It tolerates the tee's async copy goroutine.
func readPipeUntil(t *testing.T, r *os.File, want string) string {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	var out []byte
	buf := make([]byte, 4096)
	for time.Now().Before(deadline) {
		_ = r.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
		n, err := r.Read(buf)
		if n > 0 {
			out = append(out, buf[:n]...)
			if strings.Contains(string(out), want) {
				return string(out)
			}
		}
		if err != nil && n == 0 {
			continue // timeout: retry until deadline
		}
	}
	return string(out)
}