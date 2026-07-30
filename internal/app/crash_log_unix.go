// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build unix

package app

import (
	"io"
	"os"
	"sync"

	"golang.org/x/sys/unix"
)

// teeStderr redirects fd 2 through a pipe so everything written to standard
// error — including Go runtime fatal errors (e.g. "concurrent map writes"),
// which bypass os.Stderr and write straight to fd 2 — is captured into f.
// Bytes are additionally forwarded to the original stderr ONLY while no
// full-screen TUI owns the terminal (ownsScreen reports raw-mode
// ownership): while the TUI is active, a stray write — e.g. macOS libmalloc
// "MallocStackLogging" warnings, or output from child processes inheriting
// fd 2 — would be echoed mid-frame and corrupt the differential render.
// Returns a cleanup that restores fd 2, drains the pipe into f, and
// closes f.
func teeStderr(f *os.File, ownsScreen func() bool) func() {
	// Preserve the current stderr for forwarding and later restore.
	saved, err := unix.Dup(unix.Stderr)
	if err != nil {
		return noOpCloser(f)
	}
	r, w, err := os.Pipe()
	if err != nil {
		_ = unix.Close(saved)
		return noOpCloser(f)
	}
	// The runtime's fatal-error path writes to fd 2 with a raw write(2) and
	// drops bytes on EAGAIN. os.Pipe marks its descriptors non-blocking for
	// the runtime poller, so make the write end blocking before it becomes
	// fd 2 — otherwise a large goroutine dump could be truncated.
	_ = unix.SetNonblock(int(w.Fd()), false)
	if err := unix.Dup2(int(w.Fd()), unix.Stderr); err != nil {
		_ = r.Close()
		_ = w.Close()
		_ = unix.Close(saved)
		return noOpCloser(f)
	}

	savedFile := os.NewFile(uintptr(saved), "original-stderr")
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stderrSink{tty: savedFile, file: f, ownsScreen: ownsScreen}, r)
	}()

	return func() {
		// Restore fd 2 first so later writes bypass the pipe entirely. The
		// dup2 drops fd 2's reference to the pipe write end.
		_ = unix.Dup2(saved, unix.Stderr)
		// Closing the last pipe write descriptor gives the copy goroutine
		// EOF; wait for it to drain buffered bytes into f before closing.
		_ = w.Close()
		wg.Wait()
		_ = r.Close()
		_ = savedFile.Close()
		_ = f.Close()
	}
}

// stderrSink is the tee's drain destination: the crash log always receives
// every captured byte; the original terminal receives them only while no
// full-screen TUI owns it (nil ownsScreen = never owns = always forward).
type stderrSink struct {
	tty        *os.File
	file       *os.File
	ownsScreen func() bool
}

func (s *stderrSink) Write(p []byte) (int, error) {
	n, err := s.file.Write(p)
	if s.ownsScreen == nil || !s.ownsScreen() {
		_, _ = s.tty.Write(p)
	}
	return n, err
}
