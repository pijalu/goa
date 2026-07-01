// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package version

// version is set at build time via -ldflags.
var version = "dev"

// Version returns the current Goa version.
func Version() string {
	return version
}
