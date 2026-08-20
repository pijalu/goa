// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build windows

package internal

import (
	"fmt"
	"os/exec"
	"syscall"
)

// Signal is unsupported on Windows: PTY process-group signaling has no direct
// equivalent. Sessions are closed with Stop instead.
func (pm *PTYManager) Signal(id string, sig syscall.Signal) error {
	return fmt.Errorf("signals are not supported on this platform; use close")
}

// killSessionTree terminates the session process on Windows (process-group
// semantics have no direct equivalent).
func killSessionTree(cmd *exec.Cmd) {
	if cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
