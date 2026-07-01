//go:build linux
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox
import (
	"syscall"
)

// PR_SET_DUMPABLE from linux/prctl.h
const prSetDumpable = 4

func init() {
	RegisterHardenParent(func() bool {
		return syscall.Prctl(prSetDumpable, 0, 0, 0, 0) == nil
	})
}
