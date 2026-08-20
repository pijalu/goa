// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build !windows

package tools

import "syscall"

// terminalSignalValues maps the terminals tool's signal enum to platform
// signals. SIGTSTP exists only on unix-like systems, so the mapping lives in
// a build-tagged file.
var terminalSignalValues = map[string]syscall.Signal{
	"SIGINT":  syscall.SIGINT,
	"SIGTERM": syscall.SIGTERM,
	"SIGKILL": syscall.SIGKILL,
	"SIGTSTP": syscall.SIGTSTP,
	"SIGHUP":  syscall.SIGHUP,
}
