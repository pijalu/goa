// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build linux

package app

import "golang.org/x/sys/unix"

// getTermiosReq is the ioctl request that reads a terminal's termios state.
// Linux uses TCGETS (the BSD TIOCGETA name does not exist there); a tty
// answers either, so the isTty probe semantics are unchanged.
const getTermiosReq = unix.TCGETS
