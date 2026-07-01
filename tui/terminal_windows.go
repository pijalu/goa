//go:build windows
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui
import (
	"os"

	"golang.org/x/sys/windows"
)

func enableWindowsVTInput() {
	stdin := windows.Handle(os.Stdin.Fd())
	var mode uint32
	_ = windows.GetConsoleMode(stdin, &mode)
	mode |= windows.ENABLE_VIRTUAL_TERMINAL_INPUT
	_ = windows.SetConsoleMode(stdin, mode)
}
