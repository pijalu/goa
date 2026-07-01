//go:build linux
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox
import (
	"os/exec"
	"syscall"
)

func applyPreexec(cmd *exec.Cmd, fn func() error) {
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Setpgid:   true,
		Pdeathsig: syscall.SIGKILL,
	}
	_ = fn
}
