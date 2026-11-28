//go:build !windows

// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package tui

func enableWindowsVTInput() {}

// enableWindowsVTOutput is a no-op outside Windows: Unix terminals process
// VT output sequences natively.
func enableWindowsVTOutput() {}

// setWindowsUTF8OutputCP is a no-op outside Windows: Unix terminals decode
// output as UTF-8 by convention, so there is no code page to switch and
// nothing to restore.
func setWindowsUTF8OutputCP() func() { return nil }
