//go:build windows

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main

import "syscall"

// rusageNow is a stub on Windows: syscall.Getrusage does not exist there and
// the Windows syscall.Rusage layout (Filetime fields) differs from the Unix
// one. CPU accounting degrades to zero rather than failing the profile run.
func rusageNow() *syscall.Rusage {
	return &syscall.Rusage{}
}

// rusageSeconds always reports 0 on Windows — see rusageNow.
func rusageSeconds(r *syscall.Rusage) float64 {
	_ = r // Windows Rusage carries Filetime fields; no direct Unix equivalent.
	return 0
}
