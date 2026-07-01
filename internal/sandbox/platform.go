// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox

import "runtime"

func isWindows() bool {
	return runtime.GOOS == "windows"
}
