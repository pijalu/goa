// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package role defines canonical agent role identifiers used across Goa.
// Centralising them prevents typo-induced silent mis-routing and makes
// switch statements refactor-safe.
package role

// Canonical role identifiers.
const (
	Main      = "main"
	Companion = "companion"
	Planner   = "planner"
	Coder     = "coder"
	Reviewer  = "reviewer"
)

// ValidRoles is the set of all known role identifiers.
var ValidRoles = map[string]struct{}{
	Main:      {},
	Companion: {},
	Planner:   {},
	Coder:     {},
	Reviewer:  {},
}

// IsValid reports whether s is a known role identifier.
func IsValid(s string) bool {
	_, ok := ValidRoles[s]
	return ok
}
