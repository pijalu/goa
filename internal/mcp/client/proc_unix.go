// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build unix

package client

import (
	"os/exec"

	"syscall"
)

// killProcessTree sends SIGKILL to the process group of the given PID,
// terminating the process and all its descendants. On unix systems the
// MCP server child process is started in its own process group (via
// Setpgid), so killing the group cleans up the entire tree.
func killProcessTree(pid int) {
	// Kill the entire process group. The negative PID targets the group.
	// Errors are ignored — the process may have already exited.
	_ = syscall.Kill(-pid, syscall.SIGKILL)
}

// setProcGroup configures cmd to run in its own process group so that
// killProcessTree can target the entire group on Close.
func setProcGroup(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
}
