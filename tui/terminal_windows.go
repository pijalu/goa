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

// cpUTF8 is the Windows code page identifier for UTF-8.
const cpUTF8 = 65001

// Code page entry points, indirected so tests can substitute fakes.
var (
	getConsoleOutputCP = windows.GetConsoleOutputCP
	setConsoleOutputCP = windows.SetConsoleOutputCP
)

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

// setWindowsUTF8OutputCP switches the console output code page to UTF-8
// (CP_UTF8, 65001) for the duration of the TUI session so the mascot/header
// art — UTF-8 block glyphs written as a raw byte stream — is decoded
// correctly on consoles whose OEM code page is not UTF-8 (legacy conhost
// defaults to CP437/CP850, rendering the art as mojibake).
//
// It returns a restore function that sets the previous output code page
// back; the caller must run it on session stop. The restore is nil when
// nothing was changed: the current page was already UTF-8, or either call
// failed (e.g. stdout is not a console) — in both cases there is nothing
// to restore.
func setWindowsUTF8OutputCP() (restore func()) {
	prev, err := getConsoleOutputCP()
	if err != nil {
		return nil // not a console: nothing to restore
	}
	if prev == cpUTF8 {
		return nil // already UTF-8: leave it untouched
	}
	if err := setConsoleOutputCP(cpUTF8); err != nil {
		return nil // could not switch: degrade silently, nothing to restore
	}
	return func() {
		_ = setConsoleOutputCP(prev)
	}
}
