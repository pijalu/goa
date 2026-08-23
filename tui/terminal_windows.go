//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import (
	"os"

	"golang.org/x/sys/windows"
)

// ENABLE_VIRTUAL_TERMINAL_OUTPUT is absent from golang.org/x/sys/windows
// (it only defines the INPUT variant, 0x200). This is the Windows SDK
// console-mode flag that makes the console interpret VT/ANSI output.
const windowsEnableVirtualTerminalOutput = 0x0004

func enableWindowsVTInput() {
	stdin := windows.Handle(os.Stdin.Fd())
	var mode uint32
	_ = windows.GetConsoleMode(stdin, &mode)
	mode |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
	_ = windows.SetConsoleMode(stdin, mode)
}

// enableWindowsVTOutput turns on ENABLE_VIRTUAL_TERMINAL_OUTPUT for stdout so
// the compositor's ANSI/VT byte stream (SGR colors, cursor positioning, sync
// markers) is interpreted by the console instead of echoed literally.
//
// Failure is deliberately silent: legacy Console Host before Windows 10
// 1903 does not implement VT output processing, and SetConsoleMode fails
// when stdout is not a console (redirected to a file or pipe). In both cases
// the right behavior is to keep writing VT sequences unchanged — modern
// terminals already parse them, and legacy conhost simply ignores what it
// cannot render (see docs/research/ratatui-tui-enhancements.md §4.2).
func enableWindowsVTOutput() {
	stdout := windows.Handle(os.Stdout.Fd())
	var mode uint32
	if err := windows.GetConsoleMode(stdout, &mode); err != nil {
		return // not a console (or VT unsupported): degrade silently
	}
	err := windows.SetConsoleMode(stdout, mode|windowsEnableVirtualTerminalOutput)
	_ = err // pre-1903 systems reject the flag: degrade silently
}
