// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package core

// ccBoolPtr returns a *bool for tri-state config fields in tests.
func ccBoolPtr(b bool) *bool { return &b }
