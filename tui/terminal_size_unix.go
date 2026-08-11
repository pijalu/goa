//go:build !windows

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

import "golang.org/x/term"

// readSize returns the terminal size via the TIOCGWINSZ ioctl on the tty fd.
// On Unix any fd of the tty (stdin, stdout, stderr) reports the same size, so
// the input fd is sufficient.
func (t *ProcessTerminal) readSize() (w, h int, err error) {
	return term.GetSize(t.fd)
}
