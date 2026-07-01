//go:build !linux
// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package sandbox
func init() {
	RegisterHardenParent(func() bool { return true })
}
