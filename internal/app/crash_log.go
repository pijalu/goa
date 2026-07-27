// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package app

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"time"
)

// crashLogFile is the destination for crash/panic diagnostics. It is set by
// setupCrashLog and is used directly by handleShutdown so a panic is persisted
// even if the stderr-tee path has not flushed yet.
var crashLogFile *os.File

// setupCrashLog opens the crash log destination and redirects process stderr
// into a tee so both runtime fatal errors (e.g. "concurrent map writes") and
// ordinary log messages are written to disk. The returned cleanup function
// restores the original stderr and log writer; it is safe to call more than
// once.
func setupCrashLog(projectDir string) func() {
	path := crashLogPath(projectDir)
	_ = os.MkdirAll(filepath.Dir(path), 0o755)

	origLog := log.Writer()
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		log.SetOutput(origLog)
		return func() {}
	}
	crashLogFile = f
	log.SetOutput(f)
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
	log.Printf("goa crash log started: %s", path)

	cleanup := teeStderr(f)
	return func() {
		log.SetOutput(origLog)
		cleanup()
		crashLogFile = nil
	}
}

// writeCrashLog writes panic details directly to the crash log file. It is
// called from handleShutdown in addition to the stderr tee so the record is
// flushed even when the process is about to exit.
func writeCrashLog(r any, stack []byte) {
	if crashLogFile == nil {
		return
	}
	fmt.Fprintf(crashLogFile, "\n--- crash at %s ---\nPanic: %v\n\n%s\n",
		time.Now().Format(time.RFC3339Nano), r, stack)
}

// crashLogPath returns the crash log path. It prefers GOA_CRASH_LOG, then a
// project-local .goa/crash.log, then ~/.goa/crash.log as a fallback.
func crashLogPath(projectDir string) string {
	if env := os.Getenv("GOA_CRASH_LOG"); env != "" {
		return env
	}
	if projectDir != "" {
		return filepath.Join(projectDir, ".goa", "crash.log")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".goa", "crash.log")
	}
	return filepath.Join(home, ".goa", "crash.log")
}

// noOpCloser returns a cleanup that only closes the log file. Used on
// platforms where the stderr tee is not implemented.
func noOpCloser(f *os.File) func() {
	return func() { _ = f.Close() }
}

// discardWriter is a log sink that captures nothing. It is used as the
// fallback when crash log setup cannot open its file, preserving the prior
// behavior of not spamming the terminal with log output.
type discardWriter struct{}

func (discardWriter) Write(p []byte) (int, error) { return len(p), nil }

var _ io.Writer = discardWriter{}
