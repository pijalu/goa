//go:build linux
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox

import (
	"golang.org/x/sys/unix"
)

func init() {
	RegisterHardenParent(func() bool {
		return unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0) == nil
	})
}
