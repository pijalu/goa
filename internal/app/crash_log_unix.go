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

	// The drain goroutine is the SOLE consumer of the pipe. If it ever blocks
	// on a downstream write the 64KB pipe fills and EVERY fd-2 writer then
	// blocks — Go runtime fatal-error/dump writes (SIGQUIT), recovered panics,
	// and any child process that inherited fd 2 (sub-agents, background goa)
	// all freeze, and the deadlock is self-sustaining (the runtime's own dump
	// path deadlocks, so the process can't even report it). This was observed
	// as a multi-process UI freeze after a display-sleep TTY stall.
	//
	// To make the drain unblockable, the forwarded-to terminal descriptor is
	// put in non-blocking mode and the sink DROPS bytes on EAGAIN instead of
	// waiting (crash.log always receives every byte, so nothing is lost — the
	// TTY echo is best-effort). A stalled/asleep terminal can therefore never
	// backpressure the pipe.
	savedFile := os.NewFile(uintptr(saved), "original-stderr")
	_ = unix.SetNonblock(saved, true)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		_, _ = io.Copy(&stderrSink{ttyFd: saved, file: f, ownsScreen: ownsScreen}, r)
	}()

	return func() {
		// Restore the saved descriptor's blocking mode before handing it back
		// to fd 2 (we set it non-blocking for the lossy forward); the process's
		// post-tee stderr must behave exactly as it did before setup.
		_ = unix.SetNonblock(saved, false)
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
// ttyFd is the raw saved-stderr descriptor (non-blocking); it is written with
// unix.Write directly — NOT via os.File — so a full terminal returns EAGAIN
// immediately instead of parking on Go's netpoller (os.File.Write on a
// poller-managed fd would block, defeating the non-blocking guarantee).
type stderrSink struct {
	ttyFd      int
	file       *os.File
	ownsScreen func() bool
}

// Write forwards captured stderr to the crash log (always) and to the
// original terminal (only while no full-screen TUI owns it). The terminal
// descriptor is non-blocking: a short write or EAGAIN/EWOULDBLOCK (terminal
// output queue full — e.g. display asleep) drops the remaining bytes rather
// than stalling the sole pipe-drain goroutine. The crash log already holds
// every byte, so the drop loses nothing diagnostic. The drain must never
// block: if it did, the stderr pipe would fill and freeze every fd-2 writer.
func (s *stderrSink) Write(p []byte) (int, error) {
	n, err := s.file.Write(p)
	if s.ownsScreen == nil || !s.ownsScreen() {
		// Best-effort echo to the terminal. The fd is non-blocking, so this
		// returns promptly even when the terminal is stalled; on a partial
		// write or would-block (EAGAIN) we simply drop the rest.
		_, _ = unix.Write(s.ttyFd, p)
	}
	return n, err
}
