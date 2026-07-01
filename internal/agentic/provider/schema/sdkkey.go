// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

package schema

// OptionsNamespace selects where request options are placed.
type OptionsNamespace string

const (
	OptionsNamespaceRoot      OptionsNamespace = "root"
	OptionsNamespaceExtraBody OptionsNamespace = "extra_body"
	OptionsNamespaceProvider  OptionsNamespace = "provider"
)

// SdkKeyConfig describes options-namespace routing for providers that require
// non-standard parameter placement (e.g., Anthropic provider-specific options).
type SdkKeyConfig struct {
	Namespace OptionsNamespace  `json:"namespace,omitempty"`
	Mappings  map[string]string `json:"mappings,omitempty"`
}
