// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package goal

import "strings"

// contains is a small shared test helper used by the goal package tests.
func contains(s, substr string) bool { return strings.Contains(s, substr) }
