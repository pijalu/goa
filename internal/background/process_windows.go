//go:build windows
//
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package background

import (
	"os/exec"
	"strconv"
	"syscall"

	"golang.org/x/sys/windows"
)

// setSysProcAttr creates a new process group so Ctrl-Break / taskkill can
// address the whole tree.
func setSysProcAttr(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NEW_PROCESS_GROUP,
	}
}

// pidAlive reports whether the process is still running via GetExitCodeProcess.
// STILL_ACTIVE is the documented Windows constant (259) and is not exposed
// by the pinned golang.org/x/sys/windows version, so it is defined locally.
func pidAlive(pid int) bool {
	if pid <= 0 {
		return false
	}
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, uint32(pid))
	if err != nil {
		return false
	}
	defer windows.CloseHandle(h)
	var code uint32
	if err := windows.GetExitCodeProcess(h, &code); err != nil {
		return false
	}
	const stillActive = 259 // STILL_ACTIVE
	return code == stillActive
}

// signalProcess sends a Ctrl-Break (graceful) or taskkill /F (forceful) to the
// process tree. The force flag mirrors the SIGKILL escalation used on Unix.
func signalProcess(pid int, force bool) error {
	if pid <= 0 {
		return nil
	}
	if !force {
		return windows.GenerateConsoleCtrlEvent(windows.CTRL_BREAK_EVENT, uint32(pid))
	}
	return exec.Command("taskkill", "/PID", strconv.Itoa(pid), "/T", "/F").Run()
}
