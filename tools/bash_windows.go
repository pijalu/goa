//go:build windows
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tools
import (
	"os/exec"
	"strconv"
)

// configureBashCommand leaves the shell in its default process group on
// Windows.  Process-tree termination is handled by taskkill in
// killBashProcessTree.
func configureBashCommand(cmd *exec.Cmd) *exec.Cmd {
	return cmd
}

// killBashProcessTree kills the shell and any child processes using
// Windows taskkill to kill the shell and its child processes.
func killBashProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	kill := exec.Command("taskkill", "/F", "/T", "/PID", strconv.Itoa(cmd.Process.Pid))
	_ = kill.Start()
	_ = cmd.Process.Kill()
}
