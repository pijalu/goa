//go:build windows
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox
import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// applyPreexec configures the command to run without a console window.
// cmd.SysProcAttr is the stdlib *syscall.SysProcAttr, but its CreationFlags
// field is a plain uint32, so the windows.CREATE_NO_WINDOW constant value
// (defined in x/sys/windows, absent from stdlib syscall) assigns cleanly.
func applyPreexec(cmd *exec.Cmd, fn func() error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		CreationFlags: windows.CREATE_NO_WINDOW,
	}
	_ = fn
}
