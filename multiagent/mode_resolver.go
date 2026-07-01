// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package multiagent

// ModeResolver resolves mode definitions for sub-agents without importing the
// core package (avoiding an import cycle).
type ModeResolver interface {
	// Resolve returns the mode spec for the given major mode name, or an error
	// if the mode is unknown.
	Resolve(major string) (ModeSpec, error)
}

// ModeSpec describes a mode for use by sub-agent tools.
type ModeSpec struct {
	Name         string
	Body         string
	AllowedTools []string
	Temperature  float64
}
