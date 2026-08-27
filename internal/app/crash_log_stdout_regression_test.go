// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build unix

package app

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"golang.org/x/sys/unix"
)

// TestTeeStderr_DoesNotPoisonStdout is the regression test for the TUI
// rendering collapse introduced when teeStderr put the saved-stderr descriptor
// into non-blocking mode.
//
// O_NONBLOCK is a flag on the OPEN FILE DESCRIPTION, not the descriptor — and
// in a real terminal session the shell dup2s the PTY slave onto fds 0, 1 AND
// 2, so all three share ONE open file description. SetNonblock(saved-stderr,
// true) therefore silently flipped STDOUT (fd 1) to non-blocking too. Every
// multi-KB TUI frame write via os.Stdout.Write then short-wrote or failed
// with EAGAIN (Go treats a char device as blocking, so EAGAIN surfaces as a
// plain error that the render loop drops): the screen showed only the first
// couple of mascot/logo lines — the first frame truncated at the PTY output
// queue boundary — and later frames only partially landed.
//
// The helper re-runs this test binary with fds 1 and 2 wired to the SAME pipe
// (one shared open file description, exactly like a terminal session) and
// prints stdout's blocking state to fd 3 after teeStderr setup. The parent
// asserts fd 1 stayed blocking.
func TestTeeStderr_DoesNotPoisonStdout(t *testing.T) {
	if os.Getenv("GOA_TEE_HELPER") == "1" {
		runTeePoisonHelper()
		return
	}

	// Helper protocol on fd 3 (extraFiles[0]): a pipe the child writes its
	// findings to. fds 1 and 2 are wired to a single shared OFD (one pipe),
	// mimicking the shell's dup2 of the tty onto both.
	sharedR, sharedW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer sharedR.Close()
	defer sharedW.Close()
	reportR, reportW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer reportR.Close()
	defer reportW.Close()

	// #nosec G204 -- re-exec of the current test binary as a helper.
	cmd := exec.Command(os.Args[0], "-test.run", "^TestTeeStderr_DoesNotPoisonStdout$")
	cmd.Env = append(os.Environ(), "GOA_TEE_HELPER=1")
	cmd.Stdout = sharedW // fd 1 -> shared OFD
	cmd.Stderr = sharedW // fd 2 -> the SAME shared OFD (like a terminal)
	cmd.ExtraFiles = []*os.File{reportW}
	runErr := cmd.Run()
	_ = reportW.Close()
	if runErr != nil {
		t.Fatalf("helper failed: %v", runErr)
	}

	report := readAllNonblocking(t, reportR)
	t.Logf("helper report: %s", report)
	if strings.Contains(report, "STDOUT_NONBLOCKING=1") {
		t.Fatalf("REGRESSION: teeStderr made stdout non-blocking via the shared "+
			"open file description — TUI frame writes would EAGAIN/short-write.\nreport: %s", report)
	}
	if !strings.Contains(report, "STDOUT_NONBLOCKING=0") {
		t.Fatalf("helper did not report stdout blocking state; report: %q", report)
	}
}

// runTeePoisonHelper is the child side of TestTeeStderr_DoesNotPoisonStdout.
// It wires fds 1 and 2 to the shared OFD the parent supplied, runs the real
// teeStderr setup, then reports fd 1's blocking state on fd 3.
func runTeePoisonHelper() {
	report := os.NewFile(3, "report")
	if report == nil {
		return
	}
	defer report.Close()

	f, err := os.Create(filepath.Join(os.TempDir(), "goa-tee-helper-crash.log"))
	if err != nil {
		fmt.Fprintf(report, "setup: %v\n", err)
		return
	}
	cleanup := teeStderr(f, func() bool { return true })
	defer cleanup()

	fl, err := unix.FcntlInt(1, unix.F_GETFL, 0)
	if err != nil {
		fmt.Fprintf(report, "fcntl(1): %v\n", err)
		return
	}
	nb := 0
	if fl&unix.O_NONBLOCK != 0 {
		nb = 1
	}
	fmt.Fprintf(report, "STDOUT_NONBLOCKING=%d\n", nb)
}

// readAllNonblocking drains r until EOF or a short deadline, returning what
// was read. Used for the helper's fd-3 report pipe.
func readAllNonblocking(t *testing.T, r *os.File) string {
	t.Helper()
	var sb strings.Builder
	buf := make([]byte, 4096)
	deadline := 100
	for i := 0; i < deadline; i++ {
		_ = r.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		n, err := r.Read(buf)
		if n > 0 {
			sb.Write(buf[:n])
		}
		if err != nil && n == 0 {
			// timeout or EOF; if we've already got the marker we can stop
			if strings.Contains(sb.String(), "STDOUT_NONBLOCKING=") {
				break
			}
		}
	}
	return sb.String()
}
