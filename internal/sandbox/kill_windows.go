//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox

import (
	"os/exec"

	"golang.org/x/sys/windows"
)

// killProcessGroup sends a Ctrl-Break event to the process group and then
// hard-kills the process. GenerateConsoleCtrlEvent lives in x/sys/windows,
// not the standard syscall package.
func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid))
}
