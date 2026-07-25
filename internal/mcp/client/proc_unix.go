// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build unix

package client

import (
	"os"
	"os/exec"
	"strconv"
	"strings"
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

// listDescendants returns the PIDs of all descendant processes of pid.
// Used only in tests.
func listDescendants(pid int) []int {
	out, err := exec.Command("pgrep", "-P", strconv.Itoa(pid)).Output()
	if err != nil {
		return nil
	}
	var pids []int
	for _, s := range strings.Fields(string(out)) {
		if p, err := strconv.Atoi(s); err == nil {
			pids = append(pids, p)
		}
	}
	return pids
}

// processExists reports whether a process with the given PID exists.
func processExists(pid int) bool {
	p, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	// Signal 0 checks existence without actually sending a signal.
	return p.Signal(syscall.Signal(0)) == nil
}
