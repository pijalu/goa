// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

//go:build race

package app

// isRaceDetector reports whether the race detector is enabled.
func isRaceDetector() bool { return true }
