// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build windows

package tools

import "syscall"

// terminalSignalValues maps the terminals tool's signal enum to platform
// signals. Windows defines only a subset of the POSIX signal names; the tool
// still exposes the enum but reports unsupported signals at execution time.
var terminalSignalValues = map[string]syscall.Signal{
	"SIGINT":  syscall.SIGINT,
	"SIGTERM": syscall.SIGTERM,
	"SIGKILL": syscall.SIGKILL,
	"SIGHUP":  syscall.SIGHUP,
}
