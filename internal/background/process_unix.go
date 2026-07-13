//go:build !windows
//
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package background

import (
	"os/exec"
	"syscall"
)

// setSysProcAttr puts the child in its own process group so the manager can
// signal the whole tree (reattached or live) by group id.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}

// pidAlive reports whether the process group for pid still exists. A signal-0
// probe to the negative id tests the group; ESRCH (non-nil error) means dead.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	return syscall.Kill(-pid, 0) == nil
}

// signalProcess sends a graceful (SIGTERM) or forceful (SIGKILL) signal to the
// process group, falling back to the group leader if the group is gone.
func signalProcess(pid int, force bool) error {
	if pid <= 0 {
		return nil
	}
	sig := syscall.SIGTERM
	if force {
		sig = syscall.SIGKILL
	}
	if err := syscall.Kill(-pid, sig); err != nil {
		return syscall.Kill(pid, sig)
	}
	return nil
}
