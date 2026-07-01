// SPDX-License-Identifier: GPL-3.0-or-later
//
// Copyright (C) 2026 Pierre Poissinger

// Package tools implements all native Goa tools — the agent's hands in the
// filesystem. Each tool satisfies agentic.Tool with an additional
// Documentable interface for self-documentation.
package common

// Documentable is an optional interface for tools that can document themselves
// beyond their ToolSchema description.
type Documentable interface {
	// ShortDoc returns a one-line description of the tool (≤100 chars).
	ShortDoc() string

	// LongDoc returns a detailed multi-line description with usage guidance.
	LongDoc() string

	// Examples returns example invocations for documentation.
	Examples() []string
}

// DocumentedTool pairs a tool with its documentation strings.
type DocumentedTool struct {
	Tool     any
	ShortDoc string
	LongDoc  string
	Examples []string
}
