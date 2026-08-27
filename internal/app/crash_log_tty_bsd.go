// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build unix && !linux

package app

import "golang.org/x/sys/unix"

// getTermiosReq is the ioctl request that reads a terminal's termios state.
// BSD-derived kernels (Darwin, FreeBSD, OpenBSD, NetBSD, DragonFly) use
// TIOCGETA; x/sys/unix only defines it on those platforms.
const getTermiosReq = unix.TIOCGETA
