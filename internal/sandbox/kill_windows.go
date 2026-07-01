//go:build windows
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox
import (
	"os/exec"
	"syscall"
)

func killProcessGroup(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Kill()
	_ = syscall.GenerateConsoleCtrlEvent(syscall.CTRL_BREAK_EVENT, uint32(cmd.Process.Pid))
}
