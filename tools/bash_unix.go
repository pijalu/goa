//go:build !windows
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools
import (
	"os/exec"
	"syscall"
)

// configureBashCommand puts the shell in its own process group so that a
// timeout can kill the entire process tree, not just the shell itself.
// killProcessTree sends SIGTERM then SIGKILL to the process group on POSIX systems.
func configureBashCommand(cmd *exec.Cmd) *exec.Cmd {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Setpgid = true
	return cmd
}

// killBashProcessTree kills the shell and any processes it spawned by
// sending SIGKILL to its process group.
func killBashProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	_ = syscall.Kill(-pid, syscall.SIGKILL)
	_ = cmd.Process.Kill()
}
