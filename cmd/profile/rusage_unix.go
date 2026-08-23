//go:build !windows

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package main

import "syscall"

// rusageNow snapshots the process CPU usage via getrusage(2).
func rusageNow() *syscall.Rusage {
	var r syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_SELF, &r); err != nil {
		return &syscall.Rusage{}
	}
	return &r
}

// rusageSeconds converts an rusage sample into total CPU seconds
// (user + system).
func rusageSeconds(r *syscall.Rusage) float64 {
	if r == nil {
		return 0
	}
	return float64(r.Utime.Sec) + float64(r.Utime.Usec)/1e6 +
		float64(r.Stime.Sec) + float64(r.Stime.Usec)/1e6
}
