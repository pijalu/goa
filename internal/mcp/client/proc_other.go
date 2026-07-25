// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build !unix

package client

import "os/exec"

// killProcessTree is a no-op on non-unix platforms. The SDK's Close already
// terminates the direct child process.
func killProcessTree(pid int) {}

// setProcGroup is a no-op on non-unix platforms.
func setProcGroup(cmd *exec.Cmd) {}
