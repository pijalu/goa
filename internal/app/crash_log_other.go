// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build !unix

package app

import "os"

// teeStderr is not implemented on this platform (no fd-level dup2). The crash
// log still captures `log` package output and recovered panics via
// writeCrashLog; runtime fatal errors go to the terminal only.
func teeStderr(f *os.File, _ func() bool) func() {
	return noOpCloser(f)
}
